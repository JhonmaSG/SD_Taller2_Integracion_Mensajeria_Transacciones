package model

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrAlreadyProcessed indica que el message_id ya fue procesado antes.
var ErrAlreadyProcessed = errors.New("message already processed")

// MessageID identifica de forma única cada mensaje publicado en el broker.
type MessageID string

// Kind es el tópico al que pertenece un mensaje.
type Kind string

const (
	KindMetrics    Kind = "metrics"
	KindAllocation Kind = "allocations"
	KindFailover   Kind = "failover"
)

// Valores válidos del campo type de cada tópico.
const (
	TypeMetricReport      = "METRIC_REPORT"
	TypeAllocationRequest = "ALLOCATION_REQUEST"
	TypeNodeFailure       = "NODE_FAILURE"
	TypeLeaderElected     = "LEADER_ELECTED"
)

// MaxCPUTempC es el límite superior aceptado para cpu_temp, en grados Celsius.
const MaxCPUTempC = 110.0

// Message es el contrato común de los payloads recibidos del broker.
type Message interface {
	ID() MessageID
	Kind() Kind
	Key() EventKey
	Validate() error
}

// EventKey es la clave del orden total de los eventos: (lamport_ts, node_id).
type EventKey struct {
	LamportTS LamportTime
	NodeID    NodeID
}

// Less indica si k precede a other en el orden total.
func (k EventKey) Less(other EventKey) bool {
	if k.LamportTS != other.LamportTS {
		return k.LamportTS < other.LamportTS
	}
	return k.NodeID < other.NodeID
}

// Resource describe los recursos que solicita una asignación.
type Resource struct {
	CPU           float64
	RAMMB         uint64
	BandwidthMbps float64
}

// Validate exige cpu > 0, ram > 0 y ancho de banda >= 0.
func (r Resource) Validate() error {
	if !isFinite(r.CPU) || r.CPU <= 0 {
		return errors.New("cpu must be a finite number greater than zero")
	}
	if r.RAMMB == 0 {
		return errors.New("ram must be greater than zero")
	}
	return validateBandwidth(r.BandwidthMbps)
}

// MetricReport es el payload del tópico metrics.
type MetricReport struct {
	MessageID     MessageID
	Type          string
	NodeID        NodeID
	LamportTS     LamportTime
	Timestamp     time.Time
	CPUTempC      float64
	FreeRAMMB     uint64
	BandwidthMbps float64
}

// ID devuelve el message_id del reporte.
func (m MetricReport) ID() MessageID { return m.MessageID }

// Kind devuelve el tópico metrics.
func (m MetricReport) Kind() Kind { return KindMetrics }

// Key devuelve la clave de orden del reporte.
func (m MetricReport) Key() EventKey {
	return EventKey{LamportTS: m.LamportTS, NodeID: m.NodeID}
}

// Validate comprueba el tipo, los campos obligatorios y los rangos físicos.
func (m MetricReport) Validate() error {
	if err := validateHeader(m.MessageID, m.LamportTS); err != nil {
		return err
	}
	if err := validateType(m.Type, TypeMetricReport); err != nil {
		return err
	}
	if err := requireNode(m.NodeID, "node_id"); err != nil {
		return err
	}
	if m.Timestamp.IsZero() {
		return errors.New("timestamp is required")
	}
	if !isFinite(m.CPUTempC) || m.CPUTempC < 0 || m.CPUTempC > MaxCPUTempC {
		return fmt.Errorf("cpu_temp must be between 0 and %.0f °C", MaxCPUTempC)
	}
	if m.FreeRAMMB == 0 {
		return errors.New("free_ram must be greater than zero")
	}
	return validateBandwidth(m.BandwidthMbps)
}

// Supports indica si el nodo reportado tiene RAM y ancho de banda libres para r.
// La CPU no se evalúa porque el payload de metrics no reporta CPU libre.
func (m MetricReport) Supports(r Resource) bool {
	return m.FreeRAMMB >= r.RAMMB && m.BandwidthMbps >= r.BandwidthMbps
}

// AllocationRequest es el payload del tópico allocations.
type AllocationRequest struct {
	MessageID  MessageID
	Type       string
	SourceNode NodeID
	Resource   Resource
	LamportTS  LamportTime
	SagaID     SagaID
}

// ID devuelve el message_id de la solicitud.
func (a AllocationRequest) ID() MessageID { return a.MessageID }

// Kind devuelve el tópico allocations.
func (a AllocationRequest) Kind() Kind { return KindAllocation }

// Key usa source_node como node_id, porque el payload no trae un publicador.
func (a AllocationRequest) Key() EventKey {
	return EventKey{LamportTS: a.LamportTS, NodeID: a.SourceNode}
}

// Validate comprueba el tipo, la saga, el nodo origen y los recursos pedidos.
func (a AllocationRequest) Validate() error {
	if err := validateHeader(a.MessageID, a.LamportTS); err != nil {
		return err
	}
	if err := validateType(a.Type, TypeAllocationRequest); err != nil {
		return err
	}
	if err := requireNode(a.SourceNode, "source_node"); err != nil {
		return err
	}
	if err := a.SagaID.Validate(); err != nil {
		return err
	}
	return a.Resource.Validate()
}

// FailoverEvent es el payload del tópico failover.
type FailoverEvent struct {
	MessageID      MessageID
	Type           string
	PreviousLeader NodeID
	NewLeader      NodeID
	LamportTS      LamportTime
}

// ID devuelve el message_id del evento.
func (f FailoverEvent) ID() MessageID { return f.MessageID }

// Kind devuelve el tópico failover.
func (f FailoverEvent) Kind() Kind { return KindFailover }

// Key usa new_leader como node_id, porque el payload no trae un publicador.
func (f FailoverEvent) Key() EventKey {
	return EventKey{LamportTS: f.LamportTS, NodeID: f.NewLeader}
}

// Validate acepta NODE_FAILURE o LEADER_ELECTED y exige ambos líderes.
func (f FailoverEvent) Validate() error {
	if err := validateHeader(f.MessageID, f.LamportTS); err != nil {
		return err
	}
	if f.Type != TypeNodeFailure && f.Type != TypeLeaderElected {
		return fmt.Errorf("type must be %s or %s, got %q", TypeNodeFailure, TypeLeaderElected, f.Type)
	}
	if err := requireNode(f.PreviousLeader, "previous_leader"); err != nil {
		return err
	}
	return requireNode(f.NewLeader, "new_leader")
}

func validateHeader(id MessageID, lamportTS LamportTime) error {
	if id == "" {
		return errors.New("message_id is required")
	}
	if lamportTS == 0 {
		return errors.New("lamport_ts must be greater than zero")
	}
	return nil
}

func validateType(got, want string) error {
	if got != want {
		return fmt.Errorf("type must be %s, got %q", want, got)
	}
	return nil
}

func requireNode(id NodeID, field string) error {
	if id == 0 {
		return fmt.Errorf("%s must be greater than zero", field)
	}
	return nil
}

func validateBandwidth(v float64) error {
	if !isFinite(v) || v < 0 {
		return errors.New("bandwidth must be a finite number greater or equal to zero")
	}
	return nil
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
