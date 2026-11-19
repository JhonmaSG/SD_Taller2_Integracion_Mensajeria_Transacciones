package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

// EventProcessorDeps agrupa las dependencias del procesador de eventos.
type EventProcessorDeps struct {
	Clock     *model.LamportClock
	Buffer    *model.CausalBuffer[model.Message]
	Processed ports.ProcessedMessageStore
	Metrics   ports.MetricsRepository
	Failovers ports.FailoverRepository
	Sagas     *SagaOrchestrator
	Now       func() time.Time
}

// EventProcessor recibe los mensajes del broker, los ordena por (lamport_ts,
// node_id) mediante el buffer de ventana y los aplica en orden. Solo debe
// usarse en el nodo coordinador.
//
// Un mensaje se considera procesado cuando DrainReady lo devuelve. El adaptador
// del broker debe hacer ACK solo de esos message_id. Si un mensaje falla, queda
// como cabeza del buffer y se reintenta en el siguiente DrainReady.
type EventProcessor struct {
	mu        sync.Mutex
	clock     *model.LamportClock
	buffer    *model.CausalBuffer[model.Message]
	processed ports.ProcessedMessageStore
	metrics   ports.MetricsRepository
	failovers ports.FailoverRepository
	sagas     *SagaOrchestrator
	now       func() time.Time
}

// NewEventProcessor valida las dependencias y crea el procesador.
func NewEventProcessor(deps EventProcessorDeps) (*EventProcessor, error) {
	switch {
	case deps.Clock == nil:
		return nil, errors.New("lamport clock must not be nil")
	case deps.Buffer == nil:
		return nil, errors.New("causal buffer must not be nil")
	case deps.Processed == nil:
		return nil, errors.New("processed message store must not be nil")
	case deps.Metrics == nil:
		return nil, errors.New("metrics repository must not be nil")
	case deps.Failovers == nil:
		return nil, errors.New("failover repository must not be nil")
	case deps.Sagas == nil:
		return nil, errors.New("saga orchestrator must not be nil")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &EventProcessor{
		clock:     deps.Clock,
		buffer:    deps.Buffer,
		processed: deps.Processed,
		metrics:   deps.Metrics,
		failovers: deps.Failovers,
		sagas:     deps.Sagas,
		now:       now,
	}, nil
}

// Receive valida el mensaje, actualiza el reloj de Lamport con su timestamp y lo
// encola. Devuelve accepted=false si el mensaje se descartó por llegar tarde o
// por repetir una clave que ya está en espera. Un descarte no es un error: el
// adaptador debe hacer ACK del mensaje. Un payload inválido devuelve error.
func (p *EventProcessor) Receive(msg model.Message) (bool, error) {
	if msg == nil {
		return false, errors.New("message must not be nil")
	}
	if err := msg.Validate(); err != nil {
		return false, fmt.Errorf("invalid message %s: %w", msg.ID(), err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	key := msg.Key()
	p.clock.Observe(key.LamportTS)
	if err := p.buffer.Push(key, p.now(), msg); err != nil {
		log.Printf("discarded message=%s kind=%s: %v", msg.ID(), msg.Kind(), err)
		return false, nil
	}
	return true, nil
}

// DrainReady aplica, en orden, todos los mensajes cuya ventana ya venció. Se
// detiene en el primer error y devuelve los message_id aplicados hasta ese punto.
func (p *EventProcessor) DrainReady(ctx context.Context) ([]model.MessageID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	var done []model.MessageID
	for {
		msg, ready := p.buffer.Next(now)
		if !ready {
			return done, nil
		}
		if err := p.process(ctx, msg); err != nil {
			return done, fmt.Errorf("process message %s: %w", msg.ID(), err)
		}
		p.buffer.Release()
		done = append(done, msg.ID())
	}
}

// process aplica un mensaje una vez. Los duplicados ya registrados se omiten.
// El registro de message_id se escribe después de aplicar el efecto. Si el
// proceso cae entre ambos pasos, el mensaje se aplica de nuevo, por eso cada
// efecto es idempotente (saga por saga_id, métricas por reemplazo, failover por message_id).
func (p *EventProcessor) process(ctx context.Context, msg model.Message) error {
	seen, err := p.processed.IsProcessed(ctx, msg.ID())
	if err != nil {
		return fmt.Errorf("check processed: %w", err)
	}
	if seen {
		return nil
	}
	if err := p.apply(ctx, msg); err != nil {
		return err
	}
	if err := p.processed.MarkProcessed(ctx, msg.ID()); err != nil && !errors.Is(err, model.ErrAlreadyProcessed) {
		return fmt.Errorf("mark processed: %w", err)
	}
	return nil
}

func (p *EventProcessor) apply(ctx context.Context, msg model.Message) error {
	switch m := msg.(type) {
	case model.MetricReport:
		return p.metrics.SaveLatest(ctx, m)
	case model.AllocationRequest:
		return p.sagas.Start(ctx, m)
	case model.FailoverEvent:
		return p.failovers.Record(ctx, m)
	default:
		return fmt.Errorf("unsupported message kind %s", msg.Kind())
	}
}
