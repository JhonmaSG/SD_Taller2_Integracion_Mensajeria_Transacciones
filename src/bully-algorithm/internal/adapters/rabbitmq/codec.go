package rabbitmqadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

// Topology define los nombres de las colas. Prefix permite aislar pruebas en
// un mismo broker. En producción va vacío.
type Topology struct {
	Prefix string
}

// Queue devuelve el nombre de la cola de un tópico.
func (t Topology) Queue(kind model.Kind) string {
	return t.Prefix + string(kind)
}

// Kinds son los tópicos del flujo asíncrono, en orden de declaración.
var Kinds = []model.Kind{model.KindMetrics, model.KindAllocation, model.KindFailover}

// Payloads JSON según el esquema acordado. Los campos desconocidos se rechazan.
type metricPayload struct {
	MessageID string    `json:"message_id"`
	Type      string    `json:"type"`
	NodeID    string    `json:"node_id"`
	LamportTS uint64    `json:"lamport_ts"`
	Timestamp time.Time `json:"timestamp"`
	CPUTemp   float64   `json:"cpu_temp"`
	FreeRAM   uint64    `json:"free_ram"`
	Bandwidth float64   `json:"bandwidth"`
}

type resourcePayload struct {
	CPU       float64 `json:"cpu"`
	RAM       uint64  `json:"ram"`
	Bandwidth float64 `json:"bandwidth"`
}

type allocationPayload struct {
	MessageID  string          `json:"message_id"`
	Type       string          `json:"type"`
	SourceNode string          `json:"source_node"`
	Resource   resourcePayload `json:"resource"`
	LamportTS  uint64          `json:"lamport_ts"`
	SagaID     string          `json:"saga_id"`
}

type failoverPayload struct {
	MessageID      string `json:"message_id"`
	Type           string `json:"type"`
	PreviousLeader string `json:"previous_leader"`
	NewLeader      string `json:"new_leader"`
	LamportTS      uint64 `json:"lamport_ts"`
}

// EncodeMessage valida el mensaje y devuelve su cuerpo JSON.
func EncodeMessage(msg model.Message) ([]byte, error) {
	if msg == nil {
		return nil, errors.New("message must not be nil")
	}
	if err := msg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid message %s: %w", msg.ID(), err)
	}

	var payload any
	switch m := msg.(type) {
	case model.MetricReport:
		payload = metricPayload{
			MessageID: string(m.MessageID),
			Type:      m.Type,
			NodeID:    encodeNodeKey(m.NodeID),
			LamportTS: uint64(m.LamportTS),
			Timestamp: m.Timestamp,
			CPUTemp:   m.CPUTempC,
			FreeRAM:   m.FreeRAMMB,
			Bandwidth: m.BandwidthMbps,
		}
	case model.AllocationRequest:
		payload = allocationPayload{
			MessageID:  string(m.MessageID),
			Type:       m.Type,
			SourceNode: encodeNodeKey(m.SourceNode),
			Resource:   resourcePayload{CPU: m.Resource.CPU, RAM: m.Resource.RAMMB, Bandwidth: m.Resource.BandwidthMbps},
			LamportTS:  uint64(m.LamportTS),
			SagaID:     string(m.SagaID),
		}
	case model.FailoverEvent:
		payload = failoverPayload{
			MessageID:      string(m.MessageID),
			Type:           m.Type,
			PreviousLeader: encodeNodeKey(m.PreviousLeader),
			NewLeader:      encodeNodeKey(m.NewLeader),
			LamportTS:      uint64(m.LamportTS),
		}
	default:
		return nil, fmt.Errorf("unsupported message type %T", msg)
	}
	return json.Marshal(payload)
}

// DecodeMessage convierte el cuerpo JSON de un tópico en un mensaje del dominio.
// Solo valida la forma del JSON. Las reglas de negocio se validan en el dominio.
func DecodeMessage(kind model.Kind, body []byte) (model.Message, error) {
	switch kind {
	case model.KindMetrics:
		var p metricPayload
		if err := decodeStrict(body, &p); err != nil {
			return nil, err
		}
		node, err := decodeNodeKey(p.NodeID)
		if err != nil {
			return nil, fmt.Errorf("metrics node_id: %w", err)
		}
		return model.MetricReport{
			MessageID:     model.MessageID(p.MessageID),
			Type:          p.Type,
			NodeID:        node,
			LamportTS:     model.LamportTime(p.LamportTS),
			Timestamp:     p.Timestamp,
			CPUTempC:      p.CPUTemp,
			FreeRAMMB:     p.FreeRAM,
			BandwidthMbps: p.Bandwidth,
		}, nil
	case model.KindAllocation:
		var p allocationPayload
		if err := decodeStrict(body, &p); err != nil {
			return nil, err
		}
		source, err := decodeNodeKey(p.SourceNode)
		if err != nil {
			return nil, fmt.Errorf("allocation source_node: %w", err)
		}
		return model.AllocationRequest{
			MessageID:  model.MessageID(p.MessageID),
			Type:       p.Type,
			SourceNode: source,
			Resource:   model.Resource{CPU: p.Resource.CPU, RAMMB: p.Resource.RAM, BandwidthMbps: p.Resource.Bandwidth},
			LamportTS:  model.LamportTime(p.LamportTS),
			SagaID:     model.SagaID(p.SagaID),
		}, nil
	case model.KindFailover:
		var p failoverPayload
		if err := decodeStrict(body, &p); err != nil {
			return nil, err
		}
		previous, err := decodeNodeKey(p.PreviousLeader)
		if err != nil {
			return nil, fmt.Errorf("failover previous_leader: %w", err)
		}
		next, err := decodeNodeKey(p.NewLeader)
		if err != nil {
			return nil, fmt.Errorf("failover new_leader: %w", err)
		}
		return model.FailoverEvent{
			MessageID:      model.MessageID(p.MessageID),
			Type:           p.Type,
			PreviousLeader: previous,
			NewLeader:      next,
			LamportTS:      model.LamportTime(p.LamportTS),
		}, nil
	default:
		return nil, fmt.Errorf("unknown topic %q", kind)
	}
}

// decodeStrict exige un único objeto JSON sin campos desconocidos.
func decodeStrict(body []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("decode payload: unexpected data after JSON object")
	}
	return nil
}

func encodeNodeKey(id model.NodeID) string {
	return "node-" + strconv.FormatUint(uint64(id), 10)
}

func decodeNodeKey(value string) (model.NodeID, error) {
	raw, ok := strings.CutPrefix(value, "node-")
	if !ok {
		return 0, fmt.Errorf("node key %q must have the form node-N", value)
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("node key %q must have a positive numeric suffix", value)
	}
	return model.NodeID(n), nil
}
