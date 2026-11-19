package rabbitmqadapter

import (
	"strings"
	"testing"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

var codecTestTime = time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC)

func TestCodecRoundTripsEveryTopic(t *testing.T) {
	cases := []model.Message{
		model.MetricReport{MessageID: "m-1", Type: model.TypeMetricReport, NodeID: 1, LamportTS: 48, Timestamp: codecTestTime, CPUTempC: 55.5, FreeRAMMB: 1024, BandwidthMbps: 100},
		model.AllocationRequest{MessageID: "a-1", Type: model.TypeAllocationRequest, SourceNode: 1, Resource: model.Resource{CPU: 2, RAMMB: 1024, BandwidthMbps: 100}, LamportTS: 48, SagaID: "saga-9921"},
		model.FailoverEvent{MessageID: "f-1", Type: model.TypeLeaderElected, PreviousLeader: 3, NewLeader: 2, LamportTS: 52},
	}
	for _, msg := range cases {
		body, err := EncodeMessage(msg)
		if err != nil {
			t.Fatalf("EncodeMessage(%s): %v", msg.ID(), err)
		}
		back, err := DecodeMessage(msg.Kind(), body)
		if err != nil {
			t.Fatalf("DecodeMessage(%s): %v", msg.ID(), err)
		}
		if back != msg {
			t.Fatalf("round trip %s = %+v, want %+v", msg.ID(), back, msg)
		}
	}
}

func TestEncodeUsesAgreedPayloadFields(t *testing.T) {
	body, err := EncodeMessage(model.AllocationRequest{MessageID: "a-1", Type: model.TypeAllocationRequest, SourceNode: 1, Resource: model.Resource{CPU: 2, RAMMB: 1024, BandwidthMbps: 100}, LamportTS: 48, SagaID: "saga-9921"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"message_id":"a-1"`, `"type":"ALLOCATION_REQUEST"`, `"source_node":"node-1"`, `"resource":{"cpu":2,"ram":1024,"bandwidth":100}`, `"lamport_ts":48`, `"saga_id":"saga-9921"`} {
		if !strings.Contains(string(body), field) {
			t.Fatalf("payload %s lacks %s", body, field)
		}
	}
}

func TestDecodeRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	valid := `{"message_id":"a-1","type":"ALLOCATION_REQUEST","source_node":"node-1","resource":{"cpu":1,"ram":512,"bandwidth":10},"lamport_ts":7,"saga_id":"s"}`
	if _, err := DecodeMessage(model.KindAllocation, []byte(valid)); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	unknown := strings.Replace(valid, `"saga_id":"s"`, `"saga_id":"s","extra":1`, 1)
	if _, err := DecodeMessage(model.KindAllocation, []byte(unknown)); err == nil {
		t.Fatal("unknown field should be rejected")
	}
	trailing := valid + `{}`
	if _, err := DecodeMessage(model.KindAllocation, []byte(trailing)); err == nil {
		t.Fatal("trailing JSON object should be rejected")
	}
}

func TestDecodeRejectsMalformedNodeKeys(t *testing.T) {
	bad := []string{
		`{"message_id":"f-1","type":"LEADER_ELECTED","previous_leader":"node_3","new_leader":"node-2","lamport_ts":52}`,
		`{"message_id":"f-1","type":"LEADER_ELECTED","previous_leader":"node-0","new_leader":"node-2","lamport_ts":52}`,
		`{"message_id":"f-1","type":"LEADER_ELECTED","previous_leader":"node-3","new_leader":"node-x","lamport_ts":52}`,
	}
	for _, body := range bad {
		if _, err := DecodeMessage(model.KindFailover, []byte(body)); err == nil {
			t.Fatalf("payload %s should be rejected", body)
		}
	}
}

func TestEncodeRejectsInvalidMessage(t *testing.T) {
	if _, err := EncodeMessage(model.MetricReport{MessageID: "m", Type: "WRONG"}); err == nil {
		t.Fatal("invalid metric should not be encoded")
	}
	if _, err := EncodeMessage(nil); err == nil {
		t.Fatal("nil message should fail")
	}
}

func TestTopologyPrefixesQueueNames(t *testing.T) {
	if got := (Topology{}).Queue(model.KindMetrics); got != "metrics" {
		t.Fatalf("queue = %q, want metrics", got)
	}
	if got := (Topology{Prefix: "test-1-"}).Queue(model.KindFailover); got != "test-1-failover" {
		t.Fatalf("queue = %q, want test-1-failover", got)
	}
}
