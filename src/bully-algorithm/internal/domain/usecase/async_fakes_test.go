package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

var errAsyncBoom = errors.New("boom")

type asyncSagaRepo struct {
	sagas map[model.SagaID]model.Saga
}

func (r *asyncSagaRepo) Find(_ context.Context, id model.SagaID) (model.Saga, bool, error) {
	s, ok := r.sagas[id]
	return s, ok, nil
}

func (r *asyncSagaRepo) Save(_ context.Context, s *model.Saga) error {
	s.Version++
	r.sagas[s.ID] = *s
	return nil
}

func (r *asyncSagaRepo) PendingNotifications(_ context.Context) ([]model.Saga, error) {
	var out []model.Saga
	for _, s := range r.sagas {
		if s.Status == model.SagaCompleted && !s.Notified {
			out = append(out, s)
		}
	}
	return out, nil
}

type asyncMetricsRepo struct {
	latest  map[model.NodeID]model.MetricReport
	saveErr error
}

func (r *asyncMetricsRepo) SaveLatest(_ context.Context, report model.MetricReport) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.latest[report.NodeID] = report
	return nil
}

func (r *asyncMetricsRepo) Latest(_ context.Context, node model.NodeID) (model.MetricReport, bool, error) {
	m, ok := r.latest[node]
	return m, ok, nil
}

// asyncReservations responde reservas y falla las liberaciones las primeras
// releaseFailures veces con releaseErr.
type asyncReservations struct {
	reply           ports.ReservationResult
	reserveErr      error
	reserveCalls    int
	releaseErr      error
	releaseFailures int
	releaseCalls    int
}

func (r *asyncReservations) Reserve(_ context.Context, _ model.SagaID, _ model.NodeID, _ model.Resource) (ports.ReservationResult, error) {
	r.reserveCalls++
	if r.reserveErr != nil {
		return ports.ReservationResult{}, r.reserveErr
	}
	return r.reply, nil
}

func (r *asyncReservations) Release(_ context.Context, _ model.SagaID, _ model.NodeID, _ string) error {
	r.releaseCalls++
	if r.releaseCalls <= r.releaseFailures {
		return r.releaseErr
	}
	return nil
}

type asyncAllocations struct {
	err   error
	calls int
}

func (r *asyncAllocations) Replicate(_ context.Context, _ model.Saga) error {
	r.calls++
	return r.err
}

// asyncNotifier falla las primeras failures notificaciones con err.
type asyncNotifier struct {
	failures int
	err      error
	calls    int
}

func (n *asyncNotifier) NotifyAllocation(_ context.Context, _ model.SagaID, _ model.NodeID) error {
	n.calls++
	if n.calls <= n.failures {
		return n.err
	}
	return nil
}

type asyncProcessed struct {
	seen map[model.MessageID]bool
}

func (p *asyncProcessed) IsProcessed(_ context.Context, id model.MessageID) (bool, error) {
	return p.seen[id], nil
}

func (p *asyncProcessed) MarkProcessed(_ context.Context, record ports.ProcessedRecord) error {
	if p.seen[record.MessageID] {
		return model.ErrAlreadyProcessed
	}
	p.seen[record.MessageID] = true
	return nil
}

type asyncFailovers struct {
	recorded []model.FailoverEvent
}

func (f *asyncFailovers) Record(_ context.Context, e model.FailoverEvent) error {
	f.recorded = append(f.recorded, e)
	return nil
}

type asyncFixture struct {
	now          time.Time
	sleeps       []time.Duration
	sagas        *asyncSagaRepo
	metrics      *asyncMetricsRepo
	reservations *asyncReservations
	allocations  *asyncAllocations
	notifier     *asyncNotifier
	processed    *asyncProcessed
	failovers    *asyncFailovers
	clock        model.LamportClock
	orchestrator *SagaOrchestrator
}

func newAsyncFixture(t *testing.T) *asyncFixture {
	t.Helper()
	f := &asyncFixture{
		now:          time.Date(2026, 5, 10, 14, 0, 0, 0, time.UTC),
		sagas:        &asyncSagaRepo{sagas: map[model.SagaID]model.Saga{}},
		metrics:      &asyncMetricsRepo{latest: map[model.NodeID]model.MetricReport{}},
		reservations: &asyncReservations{reply: ports.ReservationResult{Accepted: true, ReservationID: "res-1"}},
		allocations:  &asyncAllocations{},
		notifier:     &asyncNotifier{},
		processed:    &asyncProcessed{seen: map[model.MessageID]bool{}},
		failovers:    &asyncFailovers{},
		clock:        model.NewLamportClock(0),
	}
	f.withNodeMetrics(1, 4096, 100)

	orch, err := NewSagaOrchestrator(SagaDependencies{
		Sagas:        f.sagas,
		Metrics:      f.metrics,
		Reservations: f.reservations,
		Allocations:  f.allocations,
		Notifier:     f.notifier,
	}, SagaOptions{
		Sleep: func(_ context.Context, d time.Duration) error {
			f.sleeps = append(f.sleeps, d)
			return nil
		},
		Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewSagaOrchestrator: %v", err)
	}
	f.orchestrator = orch
	return f
}

func (f *asyncFixture) withNodeMetrics(node model.NodeID, freeRAM uint64, bandwidth float64) {
	f.metrics.latest[node] = model.MetricReport{
		MessageID:     model.MessageID("metric-node-" + string(rune('0'+node))),
		Type:          model.TypeMetricReport,
		NodeID:        node,
		LamportTS:     1,
		Timestamp:     f.now,
		CPUTempC:      50,
		FreeRAMMB:     freeRAM,
		BandwidthMbps: bandwidth,
	}
}

func asyncAllocationRequest(sagaID model.SagaID, lamport model.LamportTime) model.AllocationRequest {
	return model.AllocationRequest{
		MessageID:  model.MessageID("alloc-" + string(sagaID)),
		Type:       model.TypeAllocationRequest,
		SourceNode: 1,
		Resource:   model.Resource{CPU: 1, RAMMB: 512, BandwidthMbps: 10},
		LamportTS:  lamport,
		SagaID:     sagaID,
	}
}

func (f *asyncFixture) newProcessor(t *testing.T, window time.Duration) *EventProcessor {
	t.Helper()
	buf, err := model.NewCausalBuffer[model.Message](window)
	if err != nil {
		t.Fatalf("NewCausalBuffer: %v", err)
	}
	p, err := NewEventProcessor(EventProcessorDeps{
		Clock:     &f.clock,
		Buffer:    buf,
		Processed: f.processed,
		Metrics:   f.metrics,
		Failovers: f.failovers,
		Sagas:     f.orchestrator,
		Now:       func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewEventProcessor: %v", err)
	}
	return p
}
