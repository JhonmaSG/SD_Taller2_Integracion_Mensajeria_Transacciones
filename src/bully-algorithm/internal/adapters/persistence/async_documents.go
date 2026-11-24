package persistence

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

// Nombres de colecciones del flujo asíncrono.
const (
	asyncSagasCollection       = "sagas"
	asyncProcessedCollection   = "processed_messages"
	asyncAllocationsCollection = "allocations"
	asyncFailoverCollection    = "failover_events"
	asyncNodeMetricsCollection = "node_metrics"
)

// Nombres de paso y estados de los pasos tal como se guardan en "sagas.steps".
const (
	asyncStepNameReserve   = "RESERVE_RESOURCE"
	asyncStepNameReplicate = "REPLICATE_ALLOCATION"
	asyncStepNameConfirm   = "CONFIRM_AND_NOTIFY"

	asyncStepPending   = "PENDING"
	asyncStepCompleted = "COMPLETED"
	asyncStepFailed    = "FAILED"
)

type asyncLamportDoc struct {
	TS     int64  `bson:"ts"`
	NodeID string `bson:"node_id"`
}

type asyncResourceDoc struct {
	CPU       float64 `bson:"cpu"`
	RAM       int64   `bson:"ram"`
	Bandwidth float64 `bson:"bandwidth"`
}

type asyncStepDoc struct {
	Step          int     `bson:"step"`
	Name          string  `bson:"name"`
	Status        string  `bson:"status"`
	ReservationID *string `bson:"reservation_id,omitempty"`
	Compensated   bool    `bson:"compensated"`
}

type asyncCompensationDoc struct {
	Required      bool    `bson:"required"`
	Step1Released bool    `bson:"step_1_released"`
	Retries       int     `bson:"retries"`
	LastError     *string `bson:"last_error"`
}

type asyncNotificationDoc struct {
	Notified  bool    `bson:"notified"`
	Retries   int     `bson:"retries"`
	LastError *string `bson:"last_error"`
}

// asyncSagaDoc es el documento de la colección "sagas". El campo version no
// está en el esquema original: es necesario para el CAS de las escrituras.
type asyncSagaDoc struct {
	SagaID        string               `bson:"saga_id"`
	Status        string               `bson:"status"`
	FailureReason *string              `bson:"failure_reason"`
	SourceNode    string               `bson:"source_node"`
	Resource      asyncResourceDoc     `bson:"resource"`
	Lamport       asyncLamportDoc      `bson:"lamport"`
	Steps         []asyncStepDoc       `bson:"steps"`
	Compensation  asyncCompensationDoc `bson:"compensation"`
	Notification  asyncNotificationDoc `bson:"notification"`
	Version       int64                `bson:"version"`
	CreatedAt     time.Time            `bson:"created_at"`
	UpdatedAt     time.Time            `bson:"updated_at"`
}

type asyncProcessedDoc struct {
	MessageID   string    `bson:"message_id"`
	Topic       string    `bson:"topic"`
	NodeID      string    `bson:"node_id"`
	LamportTS   int64     `bson:"lamport_ts"`
	ProcessedAt time.Time `bson:"processed_at"`
}

type asyncFailoverDoc struct {
	EventID        string          `bson:"event_id"`
	Type           string          `bson:"type"`
	PreviousLeader string          `bson:"previous_leader"`
	NewLeader      string          `bson:"new_leader"`
	Lamport        asyncLamportDoc `bson:"lamport"`
	RecordedAt     time.Time       `bson:"recorded_at"`
}

// asyncAllocationDoc es la asignación replicada. El esquema no la define, así
// que este documento es propuesto y debe confirmarse.
type asyncAllocationDoc struct {
	SagaID        string           `bson:"saga_id"`
	SourceNode    string           `bson:"source_node"`
	ReservationID string           `bson:"reservation_id"`
	Resource      asyncResourceDoc `bson:"resource"`
	Lamport       asyncLamportDoc  `bson:"lamport"`
	CreatedAt     time.Time        `bson:"created_at"`
}

// asyncMetricDoc guarda el último reporte de cada nodo en "node_metrics". La
// colección "nodes" no se modifica.
type asyncMetricDoc struct {
	ID            string          `bson:"_id"`
	MessageID     string          `bson:"message_id"`
	Lamport       asyncLamportDoc `bson:"lamport"`
	Timestamp     time.Time       `bson:"timestamp"`
	CPUTempC      float64         `bson:"cpu_temp_c"`
	FreeRAMMB     int64           `bson:"free_ram_mb"`
	BandwidthMbps float64         `bson:"bandwidth_mbps"`
}

// asyncNodeKey convierte un NodeID a la forma "node-N" usada en el esquema de sagas.
func asyncNodeKey(id model.NodeID) string {
	return fmt.Sprintf("node-%d", id)
}

func parseAsyncNodeKey(key string) (model.NodeID, error) {
	raw, ok := strings.CutPrefix(key, "node-")
	if !ok {
		return 0, fmt.Errorf("node key %q must have the form node-N", key)
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("node key %q must have a positive numeric suffix", key)
	}
	return model.NodeID(n), nil
}

func asyncOptionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func asyncStringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// asyncSagaToDoc convierte la saga del dominio al documento de MongoDB. El
// estado de cada paso se deriva de los campos del dominio.
func asyncSagaToDoc(s model.Saga) asyncSagaDoc {
	return asyncSagaDoc{
		SagaID:        string(s.ID),
		Status:        string(s.Status),
		FailureReason: asyncOptionalString(s.FailureReason),
		SourceNode:    asyncNodeKey(s.SourceNode),
		Resource: asyncResourceDoc{
			CPU:       s.Resource.CPU,
			RAM:       int64(s.Resource.RAMMB),
			Bandwidth: s.Resource.BandwidthMbps,
		},
		Lamport: asyncLamportDoc{TS: int64(s.LamportTS), NodeID: asyncNodeKey(s.SourceNode)},
		Steps:   asyncStepsFor(s),
		Compensation: asyncCompensationDoc{
			Required:      s.CompensationRequired,
			Step1Released: s.Status == model.SagaCompensated,
			Retries:       s.CompensationAttempts,
			LastError:     asyncOptionalString(s.CompensationLastError),
		},
		Notification: asyncNotificationDoc{
			Notified:  s.Notified,
			Retries:   s.NotificationRetries,
			LastError: asyncOptionalString(s.NotificationLastError),
		},
		Version:   int64(s.Version),
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}

// asyncStepsFor deriva el estado de los tres pasos a partir del estado de la saga.
func asyncStepsFor(s model.Saga) []asyncStepDoc {
	step1 := asyncStepDoc{
		Step:        int(model.StepReserveResource),
		Name:        asyncStepNameReserve,
		Status:      asyncStepPending,
		Compensated: s.Status == model.SagaCompensated,
	}
	if s.ReservationID != "" {
		step1.ReservationID = asyncOptionalString(s.ReservationID)
		step1.Status = asyncStepCompleted
	}
	if s.FailedStep == model.StepReserveResource {
		step1.Status = asyncStepFailed
	}

	step2 := asyncStepDoc{
		Step:   int(model.StepReplicateAllocation),
		Name:   asyncStepNameReplicate,
		Status: asyncStepPending,
	}
	switch {
	case s.FailedStep == model.StepReplicateAllocation:
		step2.Status = asyncStepFailed
	case s.Status == model.SagaCompleted:
		step2.Status = asyncStepCompleted
	}

	step3 := asyncStepDoc{
		Step:   int(model.StepConfirmAndNotify),
		Name:   asyncStepNameConfirm,
		Status: asyncStepPending,
	}
	if s.Status == model.SagaCompleted {
		step3.Status = asyncStepCompleted
	}
	return []asyncStepDoc{step1, step2, step3}
}

// asyncSagaFromDoc reconstruye la saga del dominio a partir de su documento.
func asyncSagaFromDoc(d asyncSagaDoc) (model.Saga, error) {
	source, err := parseAsyncNodeKey(d.SourceNode)
	if err != nil {
		return model.Saga{}, fmt.Errorf("saga %s source_node: %w", d.SagaID, err)
	}
	if d.Version < 0 {
		return model.Saga{}, fmt.Errorf("saga %s has negative version", d.SagaID)
	}
	s := model.Saga{
		ID:                    model.SagaID(d.SagaID),
		SourceNode:            source,
		Resource:              model.Resource{CPU: d.Resource.CPU, RAMMB: uint64(d.Resource.RAM), BandwidthMbps: d.Resource.Bandwidth},
		LamportTS:             model.LamportTime(d.Lamport.TS),
		Status:                model.SagaStatus(d.Status),
		FailureReason:         asyncStringValue(d.FailureReason),
		CompensationRequired:  d.Compensation.Required,
		CompensationAttempts:  d.Compensation.Retries,
		CompensationLastError: asyncStringValue(d.Compensation.LastError),
		Notified:              d.Notification.Notified,
		NotificationRetries:   d.Notification.Retries,
		NotificationLastError: asyncStringValue(d.Notification.LastError),
		Version:               uint64(d.Version),
		CreatedAt:             d.CreatedAt,
		UpdatedAt:             d.UpdatedAt,
	}
	for _, step := range d.Steps {
		if step.Step == int(model.StepReserveResource) {
			s.ReservationID = asyncStringValue(step.ReservationID)
		}
		if step.Status == asyncStepFailed {
			s.FailedStep = model.SagaStep(step.Step)
		}
	}
	if s.LamportTS == 0 {
		return model.Saga{}, errors.New("saga lamport ts must be greater than zero")
	}
	return s, nil
}

func asyncProcessedToDoc(r ports.ProcessedRecord) asyncProcessedDoc {
	return asyncProcessedDoc{
		MessageID:   string(r.MessageID),
		Topic:       string(r.Topic),
		NodeID:      asyncNodeKey(r.NodeID),
		LamportTS:   int64(r.LamportTS),
		ProcessedAt: r.ProcessedAt,
	}
}

func asyncFailoverToDoc(e model.FailoverEvent, recordedAt time.Time) asyncFailoverDoc {
	return asyncFailoverDoc{
		EventID:        string(e.MessageID),
		Type:           e.Type,
		PreviousLeader: asyncNodeKey(e.PreviousLeader),
		NewLeader:      asyncNodeKey(e.NewLeader),
		Lamport:        asyncLamportDoc{TS: int64(e.LamportTS), NodeID: asyncNodeKey(e.NewLeader)},
		RecordedAt:     recordedAt,
	}
}

func asyncMetricToDoc(r model.MetricReport) asyncMetricDoc {
	return asyncMetricDoc{
		ID:            asyncNodeKey(r.NodeID),
		MessageID:     string(r.MessageID),
		Lamport:       asyncLamportDoc{TS: int64(r.LamportTS), NodeID: asyncNodeKey(r.NodeID)},
		Timestamp:     r.Timestamp,
		CPUTempC:      r.CPUTempC,
		FreeRAMMB:     int64(r.FreeRAMMB),
		BandwidthMbps: r.BandwidthMbps,
	}
}

func asyncMetricFromDoc(d asyncMetricDoc) (model.MetricReport, error) {
	node, err := parseAsyncNodeKey(d.ID)
	if err != nil {
		return model.MetricReport{}, err
	}
	return model.MetricReport{
		MessageID:     model.MessageID(d.MessageID),
		Type:          model.TypeMetricReport,
		NodeID:        node,
		LamportTS:     model.LamportTime(d.Lamport.TS),
		Timestamp:     d.Timestamp,
		CPUTempC:      d.CPUTempC,
		FreeRAMMB:     uint64(d.FreeRAMMB),
		BandwidthMbps: d.BandwidthMbps,
	}, nil
}
