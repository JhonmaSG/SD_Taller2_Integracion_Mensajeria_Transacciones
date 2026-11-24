package rabbitmqadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

// fakeHandle registra los ACK y NACK recibidos.
type fakeHandle struct {
	acks    int
	nacks   int
	requeue bool
}

func (h *fakeHandle) Ack(_ bool) error {
	h.acks++
	return nil
}

func (h *fakeHandle) Nack(_ bool, requeue bool) error {
	h.nacks++
	h.requeue = requeue
	return nil
}

// fakeProcessor simula el buffer de orden: Receive guarda el mensaje y
// DrainReady devuelve los que estén listos, según ready.
type fakeProcessor struct {
	accept     bool
	receiveErr error
	drainErr   error
	buffered   []model.MessageID
	ready      bool
}

func (p *fakeProcessor) Receive(msg model.Message) (bool, error) {
	if p.receiveErr != nil {
		return false, p.receiveErr
	}
	if !p.accept {
		return false, nil
	}
	p.buffered = append(p.buffered, msg.ID())
	return true, nil
}

func (p *fakeProcessor) DrainReady(_ context.Context) ([]model.MessageID, error) {
	if p.drainErr != nil {
		return nil, p.drainErr
	}
	if !p.ready {
		return nil, nil
	}
	done := p.buffered
	p.buffered = nil
	return done, nil
}

var inboxTestMetric = []byte(`{"message_id":"m-1","type":"METRIC_REPORT","node_id":"node-1","lamport_ts":5,"timestamp":"2026-11-18T20:00:00Z","cpu_temp":50,"free_ram":1024,"bandwidth":100}`)

func TestInboxAcksOnlyAfterProcessing(t *testing.T) {
	proc := &fakeProcessor{accept: true}
	inbox := NewInbox(proc)
	h := &fakeHandle{}

	inbox.Deliver(model.KindMetrics, inboxTestMetric, h)
	if h.acks != 0 || h.nacks != 0 {
		t.Fatalf("message acked before processing: acks=%d nacks=%d", h.acks, h.nacks)
	}
	if inbox.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", inbox.Pending())
	}

	proc.ready = true
	if err := inbox.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if h.acks != 1 || inbox.Pending() != 0 {
		t.Fatalf("after drain acks=%d pending=%d, want 1 and 0", h.acks, inbox.Pending())
	}
}

func TestInboxRejectsUndecodablePayloadWithoutRequeue(t *testing.T) {
	inbox := NewInbox(&fakeProcessor{accept: true})
	h := &fakeHandle{}
	inbox.Deliver(model.KindMetrics, []byte(`{"not":"a metric"}`), h)
	if h.nacks != 1 || h.requeue {
		t.Fatalf("nacks=%d requeue=%v, want one nack without requeue", h.nacks, h.requeue)
	}
}

func TestInboxRejectsPayloadTheDomainRefuses(t *testing.T) {
	proc := &fakeProcessor{receiveErr: errors.New("invalid message")}
	inbox := NewInbox(proc)
	h := &fakeHandle{}
	inbox.Deliver(model.KindMetrics, inboxTestMetric, h)
	if h.nacks != 1 || h.requeue {
		t.Fatalf("nacks=%d requeue=%v, want nack without requeue", h.nacks, h.requeue)
	}
	if inbox.Pending() != 0 {
		t.Fatal("rejected message must not stay pending")
	}
}

func TestInboxAcksLateOrAlreadyProcessedMessageImmediately(t *testing.T) {
	inbox := NewInbox(&fakeProcessor{accept: false})
	h := &fakeHandle{}
	inbox.Deliver(model.KindMetrics, inboxTestMetric, h)
	if h.acks != 1 || h.nacks != 0 {
		t.Fatalf("acks=%d nacks=%d, want one ack", h.acks, h.nacks)
	}
}

func TestInboxRedeliveryReplacesHandleAndAcksOnce(t *testing.T) {
	proc := &fakeProcessor{accept: true, ready: true}
	inbox := NewInbox(proc)
	old := &fakeHandle{}
	fresh := &fakeHandle{}

	inbox.Deliver(model.KindMetrics, inboxTestMetric, old)
	inbox.Deliver(model.KindMetrics, inboxTestMetric, fresh)
	if old.acks != 1 {
		t.Fatalf("superseded delivery acks = %d, want 1", old.acks)
	}
	if err := inbox.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if fresh.acks != 1 {
		t.Fatalf("current delivery acks = %d, want 1", fresh.acks)
	}
	if len(proc.buffered) != 0 {
		t.Fatal("redelivery must not be received twice")
	}
}

func TestInboxKeepsMessagePendingWhenDrainFails(t *testing.T) {
	proc := &fakeProcessor{accept: true, drainErr: errors.New("mongo down")}
	inbox := NewInbox(proc)
	h := &fakeHandle{}
	inbox.Deliver(model.KindMetrics, inboxTestMetric, h)

	if err := inbox.Drain(context.Background()); err == nil {
		t.Fatal("expected drain error")
	}
	if h.acks != 0 || inbox.Pending() != 1 {
		t.Fatalf("acks=%d pending=%d, want 0 and 1", h.acks, inbox.Pending())
	}

	proc.drainErr = nil
	proc.ready = true
	if err := inbox.Drain(context.Background()); err != nil {
		t.Fatalf("Drain after recovery: %v", err)
	}
	if h.acks != 1 {
		t.Fatalf("acks after recovery = %d, want 1", h.acks)
	}
}

func TestInboxReleaseRequeuesPendingDeliveries(t *testing.T) {
	inbox := NewInbox(&fakeProcessor{accept: true})
	h := &fakeHandle{}
	inbox.Deliver(model.KindMetrics, inboxTestMetric, h)

	inbox.Release()
	if h.nacks != 1 || !h.requeue {
		t.Fatalf("nacks=%d requeue=%v, want one nack with requeue", h.nacks, h.requeue)
	}
	if inbox.Pending() != 0 {
		t.Fatal("released deliveries must leave the inbox")
	}
}

func TestRetryPolicyBackoffGrowsAndCaps(t *testing.T) {
	p := DefaultRetryPolicy()
	if got := p.backoff(1); got != 500*time.Millisecond {
		t.Fatalf("backoff(1) = %s", got)
	}
	if got := p.backoff(3); got != 2*time.Second {
		t.Fatalf("backoff(3) = %s, want 2s", got)
	}
	if got := p.backoff(20); got != 30*time.Second {
		t.Fatalf("backoff(20) = %s, want cap 30s", got)
	}
}

func TestNewConsumerAndPublisherValidateConfig(t *testing.T) {
	factory := func() (MessageProcessor, error) { return &fakeProcessor{}, nil }
	if _, err := NewConsumer(ConsumerConfig{}, factory); err == nil {
		t.Fatal("empty consumer config should fail")
	}
	if _, err := NewPublisher(PublisherConfig{}); err == nil {
		t.Fatal("empty publisher config should fail")
	}
}
