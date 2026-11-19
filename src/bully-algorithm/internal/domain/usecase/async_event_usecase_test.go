package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

func asyncMetric(id string, lamport model.LamportTime, node model.NodeID, t0 time.Time) model.MetricReport {
	return model.MetricReport{
		MessageID:     model.MessageID(id),
		Type:          model.TypeMetricReport,
		NodeID:        node,
		LamportTS:     lamport,
		Timestamp:     t0,
		CPUTempC:      50,
		FreeRAMMB:     1024,
		BandwidthMbps: 100,
	}
}

func mustReceive(t *testing.T, p *EventProcessor, msg model.Message) {
	t.Helper()
	accepted, err := p.Receive(msg)
	if err != nil {
		t.Fatalf("Receive(%s): %v", msg.ID(), err)
	}
	if !accepted {
		t.Fatalf("Receive(%s) discarded, want accepted", msg.ID())
	}
}

func equalIDs(a []model.MessageID, b ...model.MessageID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEventProcessorAppliesEventsInLamportOrderAfterWindow(t *testing.T) {
	f := newAsyncFixture(t)
	p := f.newProcessor(t, 3*time.Second)
	t0 := f.now

	mustReceive(t, p, asyncMetric("m1", 5, 2, t0))
	f.now = t0.Add(1 * time.Second)
	mustReceive(t, p, asyncMetric("m2", 5, 1, f.now))
	f.now = t0.Add(2 * time.Second)
	mustReceive(t, p, asyncMetric("m3", 4, 3, f.now))

	done, err := p.DrainReady(context.Background())
	if err != nil || len(done) != 0 {
		t.Fatalf("drain before window = %v, %v; want nothing", done, err)
	}

	f.now = t0.Add(5 * time.Second)
	done, err = p.DrainReady(context.Background())
	if err != nil {
		t.Fatalf("DrainReady: %v", err)
	}
	if !equalIDs(done, "m3", "m2", "m1") {
		t.Fatalf("applied order = %v, want [m3 m2 m1]", done)
	}
}

func TestEventProcessorDiscardsLateEventAndAcceptsLaterOne(t *testing.T) {
	f := newAsyncFixture(t)
	p := f.newProcessor(t, 0)
	t0 := f.now

	mustReceive(t, p, asyncMetric("m1", 5, 1, t0))
	if _, err := p.DrainReady(context.Background()); err != nil {
		t.Fatalf("DrainReady: %v", err)
	}

	accepted, err := p.Receive(asyncMetric("late", 4, 9, t0))
	if err != nil || accepted {
		t.Fatalf("late event accepted=%v err=%v, want discarded without error", accepted, err)
	}
	mustReceive(t, p, asyncMetric("next", 6, 1, t0))
}

func TestEventProcessorSkipsAlreadyProcessedMessage(t *testing.T) {
	f := newAsyncFixture(t)
	f.processed.seen["m1"] = true
	delete(f.metrics.latest, 1) // quita el reporte semilla de la fixture
	p := f.newProcessor(t, 0)

	mustReceive(t, p, asyncMetric("m1", 5, 1, f.now))
	done, err := p.DrainReady(context.Background())
	if err != nil {
		t.Fatalf("DrainReady: %v", err)
	}
	if !equalIDs(done, "m1") {
		t.Fatalf("done = %v, want [m1] so the broker can ACK the duplicate", done)
	}
	if _, found := f.metrics.latest[1]; found {
		t.Fatal("duplicate metric must not be applied again")
	}
}

func TestEventProcessorKeepsFailedMessageAtHeadForRetry(t *testing.T) {
	f := newAsyncFixture(t)
	p := f.newProcessor(t, 0)
	mustReceive(t, p, asyncMetric("m1", 5, 1, f.now))
	mustReceive(t, p, asyncMetric("m2", 6, 1, f.now))

	f.metrics.saveErr = errAsyncBoom
	if _, err := p.DrainReady(context.Background()); err == nil {
		t.Fatal("expected error while metrics storage fails")
	}

	f.metrics.saveErr = nil
	done, err := p.DrainReady(context.Background())
	if err != nil {
		t.Fatalf("DrainReady after recovery: %v", err)
	}
	if !equalIDs(done, "m1", "m2") {
		t.Fatalf("done = %v, want [m1 m2]", done)
	}
}

func TestEventProcessorRejectsInvalidPayload(t *testing.T) {
	f := newAsyncFixture(t)
	p := f.newProcessor(t, 0)
	bad := asyncMetric("m1", 5, 1, f.now)
	bad.CPUTempC = 999
	if _, err := p.Receive(bad); err == nil {
		t.Fatal("invalid payload should return error")
	}
	if _, err := p.Receive(nil); err == nil {
		t.Fatal("nil message should return error")
	}
}

func TestEventProcessorObservesRemoteLamportClock(t *testing.T) {
	f := newAsyncFixture(t)
	p := f.newProcessor(t, 0)
	mustReceive(t, p, asyncMetric("m1", 9, 1, f.now))
	if got := f.clock.Value(); got != 10 {
		t.Fatalf("clock = %d, want 10 (max(0,9)+1)", got)
	}
}

func TestEventProcessorStartsSagaFromAllocationMessage(t *testing.T) {
	f := newAsyncFixture(t)
	p := f.newProcessor(t, 0)
	mustReceive(t, p, asyncAllocationRequest("saga-1", 7))

	done, err := p.DrainReady(context.Background())
	if err != nil {
		t.Fatalf("DrainReady: %v", err)
	}
	if len(done) != 1 {
		t.Fatalf("done = %v, want one allocation", done)
	}
	if s := f.sagas.sagas["saga-1"]; s.Status != model.SagaCompleted {
		t.Fatalf("saga status = %s, want COMPLETED", s.Status)
	}
}

func TestEventProcessorRecordsFailover(t *testing.T) {
	f := newAsyncFixture(t)
	p := f.newProcessor(t, 0)
	event := model.FailoverEvent{
		MessageID:      "f-1",
		Type:           model.TypeLeaderElected,
		PreviousLeader: 3,
		NewLeader:      2,
		LamportTS:      9,
	}
	mustReceive(t, p, event)
	if _, err := p.DrainReady(context.Background()); err != nil {
		t.Fatalf("DrainReady: %v", err)
	}
	if len(f.failovers.recorded) != 1 || f.failovers.recorded[0].MessageID != "f-1" {
		t.Fatalf("recorded = %+v, want f-1", f.failovers.recorded)
	}
}
