package usecase

import (
	"errors"
	"fmt"
	"sync"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

// Motivos de rechazo de una reserva en el agente.
const (
	ReasonWrongNode            = "WRONG_NODE"
	ReasonInsufficientCapacity = "INSUFFICIENT_CAPACITY"
)

// ReserveOutcome es la respuesta del agente a una solicitud de reserva.
type ReserveOutcome struct {
	Accepted      bool
	ReservationID string
	Reason        string
}

type agentReservation struct {
	reservationID string
	resource      model.Resource
	committed     bool
}

// AgentReservationUseCase corre en cada nodo y lleva el libro de reservas de su
// propia capacidad. Guarda las reservas en memoria: un reinicio del nodo libera
// todas sus reservas. Es seguro para uso concurrente.
type AgentReservationUseCase struct {
	mu           sync.Mutex
	local        model.NodeID
	capacity     model.Resource
	reservations map[model.SagaID]agentReservation
}

// NewAgentReservationUseCase crea el libro de reservas del nodo local. capacity
// es la capacidad total del nodo, no la libre: el coordinador valida la
// disponibilidad real con las métricas recientes.
func NewAgentReservationUseCase(local model.NodeID, capacity model.Resource) (*AgentReservationUseCase, error) {
	if local == 0 {
		return nil, errors.New("local node id must be greater than zero")
	}
	if err := capacity.Validate(); err != nil {
		return nil, fmt.Errorf("invalid node capacity: %w", err)
	}
	return &AgentReservationUseCase{
		local:        local,
		capacity:     capacity,
		reservations: make(map[model.SagaID]agentReservation),
	}, nil
}

// Reserve reserva recursos para la saga. Es idempotente por sagaID: si la saga
// ya tiene reserva, devuelve la misma reservation_id sin volver a descontar.
func (u *AgentReservationUseCase) Reserve(sagaID model.SagaID, node model.NodeID, resource model.Resource) (ReserveOutcome, error) {
	if err := sagaID.Validate(); err != nil {
		return ReserveOutcome{}, err
	}
	if err := resource.Validate(); err != nil {
		return ReserveOutcome{}, fmt.Errorf("invalid resource: %w", err)
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if node != u.local {
		return ReserveOutcome{Accepted: false, Reason: ReasonWrongNode}, nil
	}
	if existing, ok := u.reservations[sagaID]; ok {
		return ReserveOutcome{Accepted: true, ReservationID: existing.reservationID}, nil
	}
	if !u.fits(resource) {
		return ReserveOutcome{Accepted: false, Reason: ReasonInsufficientCapacity}, nil
	}

	reservationID := "res-" + string(sagaID)
	u.reservations[sagaID] = agentReservation{reservationID: reservationID, resource: resource}
	return ReserveOutcome{Accepted: true, ReservationID: reservationID}, nil
}

// Release libera la reserva de la saga. Si no existe, no hace nada: es
// idempotente. Si reservationID no coincide o la asignación ya fue confirmada,
// devuelve error.
func (u *AgentReservationUseCase) Release(sagaID model.SagaID, reservationID string) error {
	if err := sagaID.Validate(); err != nil {
		return err
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	existing, ok := u.reservations[sagaID]
	if !ok {
		return nil
	}
	if reservationID != "" && reservationID != existing.reservationID {
		return fmt.Errorf("reservation %s does not match saga %s", reservationID, sagaID)
	}
	if existing.committed {
		return fmt.Errorf("saga %s already confirmed, reservation cannot be released", sagaID)
	}
	delete(u.reservations, sagaID)
	return nil
}

// Confirm marca la reserva de la saga como asignación confirmada. Devuelve
// false si el agente no tiene reserva para esa saga, por ejemplo tras un
// reinicio. El llamador decide si lo registra en el log.
func (u *AgentReservationUseCase) Confirm(sagaID model.SagaID) (bool, error) {
	if err := sagaID.Validate(); err != nil {
		return false, err
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	existing, ok := u.reservations[sagaID]
	if !ok {
		return false, nil
	}
	existing.committed = true
	u.reservations[sagaID] = existing
	return true, nil
}

// fits indica si el recurso cabe en la capacidad restante. Debe llamarse con u.mu tomado.
func (u *AgentReservationUseCase) fits(resource model.Resource) bool {
	var used model.Resource
	for _, held := range u.reservations {
		used.CPU += held.resource.CPU
		used.RAMMB += held.resource.RAMMB
		used.BandwidthMbps += held.resource.BandwidthMbps
	}
	return used.CPU+resource.CPU <= u.capacity.CPU &&
		used.RAMMB+resource.RAMMB <= u.capacity.RAMMB &&
		used.BandwidthMbps+resource.BandwidthMbps <= u.capacity.BandwidthMbps
}
