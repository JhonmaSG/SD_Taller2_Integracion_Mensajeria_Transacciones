package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

const reasonReservationRejected = "RESERVATION_REJECTED"

// CompensationPolicy define los reintentos de la compensación del paso 1.
// El espera entre intentos crece de forma exponencial desde InitialBackoff y
// se limita a MaxBackoff.
type CompensationPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// DefaultCompensationPolicy devuelve 5 intentos, de 1 s a 30 s de espera.
func DefaultCompensationPolicy() CompensationPolicy {
	return CompensationPolicy{
		MaxAttempts:    5,
		InitialBackoff: time.Second,
		MaxBackoff:     30 * time.Second,
	}
}

func (p CompensationPolicy) validate() error {
	if p.MaxAttempts < 1 {
		return errors.New("compensation max attempts must be at least one")
	}
	if p.InitialBackoff <= 0 {
		return errors.New("compensation initial backoff must be greater than zero")
	}
	if p.MaxBackoff < p.InitialBackoff {
		return errors.New("compensation max backoff must not be below initial backoff")
	}
	return nil
}

// backoff devuelve la espera tras el intento número attempt (desde 1).
func (p CompensationPolicy) backoff(attempt int) time.Duration {
	delay := p.InitialBackoff
	for i := 1; i < attempt && delay < p.MaxBackoff; i++ {
		delay *= 2
	}
	if delay > p.MaxBackoff {
		return p.MaxBackoff
	}
	return delay
}

// SagaDependencies agrupa los puertos que necesita el orquestador.
type SagaDependencies struct {
	Sagas        ports.SagaRepository
	Metrics      ports.MetricsRepository
	Reservations ports.ResourceReservationPort
	Allocations  ports.AllocationRepository
	Notifier     ports.AgentNotifier
}

// SagaOptions contiene parámetros opcionales. Los campos nil usan valores reales.
type SagaOptions struct {
	Compensation CompensationPolicy
	Sleep        func(ctx context.Context, d time.Duration) error
	Now          func() time.Time
}

// SagaOrchestrator ejecuta la saga de asignación del coordinador. Sus pasos son:
//  1. Reservar el recurso en el nodo origen (compensación: liberar la reserva).
//  2. Replicar la asignación en el Replica Set.
//  3. Confirmar (COMPLETED) y notificar al agente. Si la notificación falla, la
//     saga queda COMPLETED con notified=false y se reenvía con RetryPendingNotifications.
type SagaOrchestrator struct {
	deps         SagaDependencies
	compensation CompensationPolicy
	sleep        func(ctx context.Context, d time.Duration) error
	now          func() time.Time
}

// NewSagaOrchestrator valida dependencias y opciones y crea el orquestador.
func NewSagaOrchestrator(deps SagaDependencies, opts SagaOptions) (*SagaOrchestrator, error) {
	switch {
	case deps.Sagas == nil:
		return nil, errors.New("saga repository must not be nil")
	case deps.Metrics == nil:
		return nil, errors.New("metrics repository must not be nil")
	case deps.Reservations == nil:
		return nil, errors.New("reservation port must not be nil")
	case deps.Allocations == nil:
		return nil, errors.New("allocation repository must not be nil")
	case deps.Notifier == nil:
		return nil, errors.New("agent notifier must not be nil")
	}
	if opts.Compensation == (CompensationPolicy{}) {
		opts.Compensation = DefaultCompensationPolicy()
	}
	if err := opts.Compensation.validate(); err != nil {
		return nil, err
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepContext
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &SagaOrchestrator{
		deps:         deps,
		compensation: opts.Compensation,
		sleep:        opts.Sleep,
		now:          opts.Now,
	}, nil
}

// Start valida la solicitud y ejecuta la saga. Si ya existe una saga con ese
// saga_id, no hace nada: la solicitud ya fue tratada.
func (o *SagaOrchestrator) Start(ctx context.Context, req model.AllocationRequest) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("invalid allocation request: %w", err)
	}
	_, exists, err := o.deps.Sagas.Find(ctx, req.SagaID)
	if err != nil {
		return fmt.Errorf("find saga %s: %w", req.SagaID, err)
	}
	if exists {
		log.Printf("saga=%s duplicate request ignored", req.SagaID)
		return nil
	}
	saga, err := model.NewSaga(req.SagaID, req.SourceNode, req.Resource, req.LamportTS, o.now())
	if err != nil {
		return err
	}
	log.Printf("saga=%s STARTED source=node-%d ram=%d cpu=%g bandwidth=%g lamport=%d",
		saga.ID, saga.SourceNode, saga.Resource.RAMMB, saga.Resource.CPU, saga.Resource.BandwidthMbps, saga.LamportTS)
	// La saga se persiste antes de cualquier efecto externo para poder retomarla.
	if err := o.save(ctx, &saga); err != nil {
		return err
	}
	return o.execute(ctx, &saga)
}

// RetryPendingNotifications reenvía la notificación de las sagas COMPLETED
// que aún no tienen notified=true. Continúa con las demás si una falla.
func (o *SagaOrchestrator) RetryPendingNotifications(ctx context.Context) error {
	pending, err := o.deps.Sagas.PendingNotifications(ctx)
	if err != nil {
		return fmt.Errorf("list pending notifications: %w", err)
	}
	var errs []error
	for i := range pending {
		saga := &pending[i]
		if err := o.deps.Notifier.NotifyAllocation(ctx, saga.ID, saga.SourceNode); err != nil {
			errs = append(errs, fmt.Errorf("notify saga %s: %w", saga.ID, err))
			if recErr := saga.RecordNotificationFailure(err.Error(), o.now()); recErr == nil {
				if saveErr := o.save(ctx, saga); saveErr != nil {
					errs = append(errs, saveErr)
				}
			}
			continue
		}
		if err := saga.MarkNotified(o.now()); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := o.save(ctx, saga); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (o *SagaOrchestrator) execute(ctx context.Context, saga *model.Saga) error {
	snapshot, found, err := o.deps.Metrics.Latest(ctx, saga.SourceNode)
	if err != nil {
		return fmt.Errorf("read metrics of node %d: %w", saga.SourceNode, err)
	}
	if !found {
		return o.reject(ctx, saga, model.ReasonNodeMetricsUnavailable)
	}
	if !snapshot.Supports(saga.Resource) {
		return o.reject(ctx, saga, model.ReasonInsufficientResources)
	}

	log.Printf("saga=%s metrics ok: node-%d free_ram=%d bandwidth=%g", saga.ID, saga.SourceNode, snapshot.FreeRAMMB, snapshot.BandwidthMbps)

	// Paso 1: reservar en el nodo origen.
	result, err := o.deps.Reservations.Reserve(ctx, saga.ID, saga.SourceNode, saga.Resource)
	if err != nil {
		// Sin respuesta no se sabe si el agente reservó. La liberación por saga_id es
		// idempotente, así que se compensa igualmente.
		log.Printf("saga=%s reserve failed, compensating: %v", saga.ID, err)
		return o.compensate(ctx, saga, model.StepReserveResource)
	}
	if !result.Accepted {
		reason := result.Reason
		if reason == "" {
			reason = reasonReservationRejected
		}
		return o.reject(ctx, saga, reason)
	}
	if err := saga.MarkReserved(result.ReservationID, o.now()); err != nil {
		log.Printf("saga=%s invalid reservation response, compensating: %v", saga.ID, err)
		return o.compensate(ctx, saga, model.StepReserveResource)
	}
	if err := o.save(ctx, saga); err != nil {
		return err
	}

	log.Printf("saga=%s step=1 RESERVE_RESOURCE completed: reservation=%s", saga.ID, saga.ReservationID)

	// Paso 2: replicar la asignación. Si falla, se compensa el paso 1.
	if err := o.deps.Allocations.Replicate(ctx, *saga); err != nil {
		log.Printf("saga=%s replicate failed, compensating: %v", saga.ID, err)
		return o.compensate(ctx, saga, model.StepReplicateAllocation)
	}

	log.Printf("saga=%s step=2 REPLICATE_ALLOCATION completed", saga.ID)

	// Paso 3: confirmar y notificar. Un fallo de notificación no revierte la asignación.
	if err := saga.Complete(o.now()); err != nil {
		return err
	}
	if err := o.save(ctx, saga); err != nil {
		return err
	}
	if err := o.deps.Notifier.NotifyAllocation(ctx, saga.ID, saga.SourceNode); err != nil {
		log.Printf("saga=%s completed, notification pending: %v", saga.ID, err)
		if recErr := saga.RecordNotificationFailure(err.Error(), o.now()); recErr != nil {
			return recErr
		}
		return o.save(ctx, saga)
	}
	log.Printf("saga=%s step=3 CONFIRM_AND_NOTIFY: agent notified, status=COMPLETED", saga.ID)
	if err := saga.MarkNotified(o.now()); err != nil {
		return err
	}
	return o.save(ctx, saga)
}

// reject termina la saga como FAILED. No hay reserva que compensar.
func (o *SagaOrchestrator) reject(ctx context.Context, saga *model.Saga, reason string) error {
	log.Printf("saga=%s REJECTED: reason=%s, no compensation needed", saga.ID, reason)
	if err := saga.Reject(reason, o.now()); err != nil {
		return err
	}
	return o.save(ctx, saga)
}

// compensate libera la reserva con reintentos acotados. Al agotarlos, la saga
// queda en COMPENSATION_FAILED con el motivo para revisión posterior.
func (o *SagaOrchestrator) compensate(ctx context.Context, saga *model.Saga, failedStep model.SagaStep) error {
	log.Printf("saga=%s COMPENSATING: failed_step=%v, releasing reservation=%s", saga.ID, failedStep, saga.ReservationID)
	if err := saga.BeginCompensation(failedStep, o.now()); err != nil {
		return err
	}
	if err := o.save(ctx, saga); err != nil {
		return err
	}
	var lastErr error
	for attempt := 1; attempt <= o.compensation.MaxAttempts; attempt++ {
		if err := saga.RecordCompensationAttempt(o.now()); err != nil {
			return err
		}
		if err := o.save(ctx, saga); err != nil {
			return err
		}
		err := o.deps.Reservations.Release(ctx, saga.ID, saga.SourceNode, saga.ReservationID)
		if err == nil {
			log.Printf("saga=%s COMPENSATED: reservation=%s released on attempt %d", saga.ID, saga.ReservationID, attempt)
			if err := saga.MarkCompensated(o.now()); err != nil {
				return err
			}
			return o.save(ctx, saga)
		}
		lastErr = err
		log.Printf("saga=%s compensation attempt %d/%d failed: %v", saga.ID, attempt, o.compensation.MaxAttempts, err)
		if err := saga.RecordCompensationError(err.Error(), o.now()); err != nil {
			return err
		}
		if err := o.save(ctx, saga); err != nil {
			return err
		}
		if attempt < o.compensation.MaxAttempts {
			if err := o.sleep(ctx, o.compensation.backoff(attempt)); err != nil {
				return err
			}
		}
	}
	reason := fmt.Sprintf("release failed after %d attempts: %v", o.compensation.MaxAttempts, lastErr)
	log.Printf("saga=%s COMPENSATION_FAILED after %d attempts: %v", saga.ID, o.compensation.MaxAttempts, lastErr)
	if err := saga.MarkCompensationFailed(reason, o.now()); err != nil {
		return err
	}
	return o.save(ctx, saga)
}

func (o *SagaOrchestrator) save(ctx context.Context, saga *model.Saga) error {
	if err := o.deps.Sagas.Save(ctx, saga); err != nil {
		return fmt.Errorf("save saga %s: %w", saga.ID, err)
	}
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
