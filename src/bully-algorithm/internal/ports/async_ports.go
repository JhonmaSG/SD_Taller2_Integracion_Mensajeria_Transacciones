package ports

import (
	"context"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

// ProcessedRecord es el registro de un mensaje aplicado.
type ProcessedRecord struct {
	MessageID   model.MessageID
	Topic       model.Kind
	NodeID      model.NodeID
	LamportTS   model.LamportTime
	ProcessedAt time.Time
}

// ProcessedMessageStore registra los message_id ya procesados para descartar duplicados.
type ProcessedMessageStore interface {
	IsProcessed(ctx context.Context, id model.MessageID) (bool, error)
	// MarkProcessed registra el mensaje. Devuelve model.ErrAlreadyProcessed si ya existía.
	MarkProcessed(ctx context.Context, record ProcessedRecord) error
}

// MetricsRepository guarda el estado más reciente reportado por cada nodo.
type MetricsRepository interface {
	// SaveLatest reemplaza el último reporte del nodo.
	SaveLatest(ctx context.Context, report model.MetricReport) error
	// Latest devuelve el último reporte del nodo. El bool indica si existe.
	Latest(ctx context.Context, node model.NodeID) (model.MetricReport, bool, error)
}

// SagaRepository persiste el estado de cada saga.
type SagaRepository interface {
	// Find devuelve la saga con ese identificador. El bool indica si existe.
	Find(ctx context.Context, id model.SagaID) (model.Saga, bool, error)
	// Save persiste el estado de la saga con CAS sobre saga.Version. Si el
	// repositorio aceptó la escritura, actualiza saga.Version. Devuelve
	// ErrVersionConflict si otra escritura se adelantó.
	Save(ctx context.Context, saga *model.Saga) error
	// PendingNotifications devuelve las sagas COMPLETED sin notificar.
	PendingNotifications(ctx context.Context) ([]model.Saga, error)
}

// AllocationRepository replica la asignación confirmada en el Replica Set.
type AllocationRepository interface {
	// Replicate persiste la asignación. Debe ser idempotente por saga_id.
	Replicate(ctx context.Context, saga model.Saga) error
}

// FailoverRepository guarda los eventos de failover como auditoría.
type FailoverRepository interface {
	// Record persiste el evento. Debe ser idempotente por message_id.
	Record(ctx context.Context, event model.FailoverEvent) error
}

// ReservationResult es la respuesta del agente a una solicitud de reserva.
type ReservationResult struct {
	Accepted      bool
	ReservationID string
	Reason        string
}

// ResourceReservationPort reserva y libera recursos en un agente por gRPC.
type ResourceReservationPort interface {
	// Reserve es idempotente por sagaID: un reintento devuelve la misma reserva.
	Reserve(ctx context.Context, sagaID model.SagaID, node model.NodeID, resource model.Resource) (ReservationResult, error)
	// Release libera la reserva de la saga. Es idempotente por sagaID.
	Release(ctx context.Context, sagaID model.SagaID, reservationID string) error
}

// AgentNotifier envía al agente la confirmación de su asignación.
type AgentNotifier interface {
	NotifyAllocation(ctx context.Context, sagaID model.SagaID, node model.NodeID) error
}
