package model

import (
	"errors"
	"fmt"
	"time"
)

// SagaID identifica una saga de asignación. Es la clave de idempotencia de la
// reserva en los agentes.
type SagaID string

// Validate exige un identificador no vacío.
func (id SagaID) Validate() error {
	if id == "" {
		return errors.New("saga_id is required")
	}
	return nil
}

// SagaStatus es el estado persistido de una saga.
type SagaStatus string

const (
	SagaInProgress         SagaStatus = "IN_PROGRESS"
	SagaCompleted          SagaStatus = "COMPLETED"
	SagaCompensated        SagaStatus = "COMPENSATED"
	SagaCompensationFailed SagaStatus = "COMPENSATION_FAILED"
	SagaFailed             SagaStatus = "FAILED"
)

// SagaStep identifica un paso de la saga. StepNone indica que ninguno falló.
type SagaStep int

const (
	StepNone                SagaStep = 0
	StepReserveResource     SagaStep = 1
	StepReplicateAllocation SagaStep = 2
	StepConfirmAndNotify    SagaStep = 3
)

// Motivos de rechazo antes de reservar.
const (
	ReasonInsufficientResources  = "INSUFFICIENT_RESOURCES"
	ReasonNodeMetricsUnavailable = "NODE_METRICS_UNAVAILABLE"
)

// Saga es la transacción orquestada por el coordinador para una asignación.
// Las transiciones validan el estado actual y devuelven error si son ilegales.
type Saga struct {
	ID                    SagaID
	SourceNode            NodeID
	Resource              Resource
	LamportTS             LamportTime
	Status                SagaStatus
	ReservationID         string
	FailureReason         string
	FailedStep            SagaStep
	CompensationRequired  bool
	CompensationAttempts  int
	CompensationLastError string
	Notified              bool
	NotificationRetries   int
	NotificationLastError string
	// Version lo asigna el repositorio en cada escritura para el CAS.
	Version   uint64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewSaga crea una saga en estado IN_PROGRESS a partir de una solicitud validada.
func NewSaga(id SagaID, source NodeID, resource Resource, lamportTS LamportTime, at time.Time) (Saga, error) {
	if err := id.Validate(); err != nil {
		return Saga{}, err
	}
	if err := requireNode(source, "source_node"); err != nil {
		return Saga{}, err
	}
	if err := resource.Validate(); err != nil {
		return Saga{}, err
	}
	if lamportTS == 0 {
		return Saga{}, errors.New("lamport_ts must be greater than zero")
	}
	return Saga{
		ID:         id,
		SourceNode: source,
		Resource:   resource,
		LamportTS:  lamportTS,
		Status:     SagaInProgress,
		CreatedAt:  at,
		UpdatedAt:  at,
	}, nil
}

// MarkReserved registra la reserva aceptada en el nodo origen (paso 1).
func (s *Saga) MarkReserved(reservationID string, at time.Time) error {
	if err := s.requireStatus(SagaInProgress); err != nil {
		return err
	}
	if reservationID == "" {
		return errors.New("reservation id is required")
	}
	if s.ReservationID != "" {
		return errors.New("saga already holds a reservation")
	}
	s.ReservationID = reservationID
	s.UpdatedAt = at
	return nil
}

// Reject termina la saga como FAILED en el paso 1. Solo es válido si aún no hay
// reserva, porque una saga con reserva debe compensarse.
func (s *Saga) Reject(reason string, at time.Time) error {
	if err := s.requireStatus(SagaInProgress); err != nil {
		return err
	}
	if s.ReservationID != "" {
		return errors.New("cannot reject a saga that holds a reservation")
	}
	if reason == "" {
		return errors.New("rejection reason is required")
	}
	s.Status = SagaFailed
	s.FailureReason = reason
	s.FailedStep = StepReserveResource
	s.UpdatedAt = at
	return nil
}

// Complete confirma la asignación tras replicarla en MongoDB (pasos 2 y 3).
func (s *Saga) Complete(at time.Time) error {
	if err := s.requireStatus(SagaInProgress); err != nil {
		return err
	}
	if s.ReservationID == "" {
		return errors.New("a saga without reservation cannot be completed")
	}
	s.Status = SagaCompleted
	s.UpdatedAt = at
	return nil
}

// BeginCompensation marca el paso que falló y que la saga requiere compensación.
func (s *Saga) BeginCompensation(failedStep SagaStep, at time.Time) error {
	if err := s.requireStatus(SagaInProgress); err != nil {
		return err
	}
	if failedStep == StepNone {
		return errors.New("failed step is required")
	}
	s.CompensationRequired = true
	s.FailedStep = failedStep
	s.UpdatedAt = at
	return nil
}

// RecordCompensationAttempt cuenta un intento de liberar la reserva.
func (s *Saga) RecordCompensationAttempt(at time.Time) error {
	if err := s.requireStatus(SagaInProgress, SagaCompensationFailed); err != nil {
		return err
	}
	s.CompensationAttempts++
	s.UpdatedAt = at
	return nil
}

// RecordCompensationError guarda el error del último intento fallido.
func (s *Saga) RecordCompensationError(lastError string, at time.Time) error {
	if err := s.requireStatus(SagaInProgress, SagaCompensationFailed); err != nil {
		return err
	}
	s.CompensationLastError = lastError
	s.UpdatedAt = at
	return nil
}

// MarkCompensated registra que la reserva fue liberada.
func (s *Saga) MarkCompensated(at time.Time) error {
	if err := s.requireStatus(SagaInProgress, SagaCompensationFailed); err != nil {
		return err
	}
	s.Status = SagaCompensated
	s.FailureReason = ""
	s.UpdatedAt = at
	return nil
}

// MarkCompensationFailed registra que la compensación agotó sus reintentos.
func (s *Saga) MarkCompensationFailed(reason string, at time.Time) error {
	if err := s.requireStatus(SagaInProgress, SagaCompensationFailed); err != nil {
		return err
	}
	if reason == "" {
		return errors.New("compensation failure reason is required")
	}
	s.Status = SagaCompensationFailed
	s.FailureReason = reason
	s.UpdatedAt = at
	return nil
}

// MarkNotified registra que el agente recibió la notificación de una saga COMPLETED.
func (s *Saga) MarkNotified(at time.Time) error {
	if err := s.requireStatus(SagaCompleted); err != nil {
		return err
	}
	if s.Notified {
		return errors.New("saga already notified")
	}
	s.Notified = true
	s.UpdatedAt = at
	return nil
}

// RecordNotificationFailure cuenta un intento fallido de notificación.
func (s *Saga) RecordNotificationFailure(lastError string, at time.Time) error {
	if err := s.requireStatus(SagaCompleted); err != nil {
		return err
	}
	if s.Notified {
		return errors.New("saga already notified")
	}
	s.NotificationRetries++
	s.NotificationLastError = lastError
	s.UpdatedAt = at
	return nil
}

func (s *Saga) requireStatus(allowed ...SagaStatus) error {
	for _, status := range allowed {
		if s.Status == status {
			return nil
		}
	}
	return fmt.Errorf("saga %s in status %s cannot perform this transition", s.ID, s.Status)
}
