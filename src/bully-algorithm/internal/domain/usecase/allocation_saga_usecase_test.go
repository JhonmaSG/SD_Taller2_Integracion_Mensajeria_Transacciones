package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

func TestSagaCompletesAndNotifiesAgent(t *testing.T) {
	f := newAsyncFixture(t)
	req := asyncAllocationRequest("saga-1", 7)

	if err := f.orchestrator.Start(context.Background(), req); err != nil {
		t.Fatalf("Start: %v", err)
	}

	s := f.sagas.sagas["saga-1"]
	if s.Status != model.SagaCompleted || !s.Notified || s.ReservationID != "res-1" {
		t.Fatalf("saga = %+v, want COMPLETED, notified, reservation res-1", s)
	}
	if f.allocations.calls != 1 || f.notifier.calls != 1 || f.reservations.releaseCalls != 0 {
		t.Fatalf("replicate=%d notify=%d release=%d", f.allocations.calls, f.notifier.calls, f.reservations.releaseCalls)
	}
}

func TestSagaRejectsWhenNodeLacksResources(t *testing.T) {
	f := newAsyncFixture(t)
	f.withNodeMetrics(1, 100, 100) // solo 100 MB libres, se piden 512

	if err := f.orchestrator.Start(context.Background(), asyncAllocationRequest("saga-1", 7)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s := f.sagas.sagas["saga-1"]
	if s.Status != model.SagaFailed || s.FailureReason != model.ReasonInsufficientResources {
		t.Fatalf("saga = %+v, want FAILED INSUFFICIENT_RESOURCES", s)
	}
	if f.reservations.reserveCalls != 0 {
		t.Fatal("no reservation should be attempted when resources are insufficient")
	}
}

func TestSagaRejectsWhenNodeHasNoMetrics(t *testing.T) {
	f := newAsyncFixture(t)
	delete(f.metrics.latest, 1)

	if err := f.orchestrator.Start(context.Background(), asyncAllocationRequest("saga-1", 7)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s := f.sagas.sagas["saga-1"]
	if s.Status != model.SagaFailed || s.FailureReason != model.ReasonNodeMetricsUnavailable {
		t.Fatalf("saga = %+v, want FAILED NODE_METRICS_UNAVAILABLE", s)
	}
}

func TestSagaFailsWithoutCompensationWhenAgentRejectsReservation(t *testing.T) {
	f := newAsyncFixture(t)
	f.reservations.reply = ports.ReservationResult{Accepted: false, Reason: "AGENT_BUSY"}

	if err := f.orchestrator.Start(context.Background(), asyncAllocationRequest("saga-1", 7)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s := f.sagas.sagas["saga-1"]
	if s.Status != model.SagaFailed || s.FailureReason != "AGENT_BUSY" {
		t.Fatalf("saga = %+v, want FAILED AGENT_BUSY", s)
	}
	if f.reservations.releaseCalls != 0 {
		t.Fatal("a rejected reservation holds nothing and must not be released")
	}
}

func TestSagaCompensatesReservationWhenReplicationFails(t *testing.T) {
	f := newAsyncFixture(t)
	f.allocations.err = errAsyncBoom

	if err := f.orchestrator.Start(context.Background(), asyncAllocationRequest("saga-1", 7)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s := f.sagas.sagas["saga-1"]
	if s.Status != model.SagaCompensated {
		t.Fatalf("status = %s, want COMPENSATED", s.Status)
	}
	if f.reservations.releaseCalls != 1 {
		t.Fatalf("release calls = %d, want 1", f.reservations.releaseCalls)
	}
	if f.notifier.calls != 0 {
		t.Fatal("agent must not be notified of a compensated saga")
	}
}

func TestSagaCompensatesWhenReserveCallTimesOut(t *testing.T) {
	f := newAsyncFixture(t)
	f.reservations.reserveErr = errAsyncBoom

	if err := f.orchestrator.Start(context.Background(), asyncAllocationRequest("saga-1", 7)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s := f.sagas.sagas["saga-1"]
	if s.Status != model.SagaCompensated {
		t.Fatalf("status = %s, want COMPENSATED", s.Status)
	}
	if f.reservations.releaseCalls != 1 {
		t.Fatalf("release calls = %d, want 1 (idempotent release by saga_id)", f.reservations.releaseCalls)
	}
}

func TestCompensationRetriesWithExponentialBackoffThenFails(t *testing.T) {
	f := newAsyncFixture(t)
	f.allocations.err = errAsyncBoom
	f.reservations.releaseErr = errAsyncBoom
	f.reservations.releaseFailures = 100 // la liberación nunca funciona

	if err := f.orchestrator.Start(context.Background(), asyncAllocationRequest("saga-1", 7)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s := f.sagas.sagas["saga-1"]
	if s.Status != model.SagaCompensationFailed {
		t.Fatalf("status = %s, want COMPENSATION_FAILED", s.Status)
	}
	if s.CompensationAttempts != 5 || f.reservations.releaseCalls != 5 {
		t.Fatalf("attempts=%d releases=%d, want 5", s.CompensationAttempts, f.reservations.releaseCalls)
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	if len(f.sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", f.sleeps, want)
	}
	for i := range want {
		if f.sleeps[i] != want[i] {
			t.Fatalf("sleeps = %v, want %v", f.sleeps, want)
		}
	}
}

func TestCompensationSucceedsOnRetry(t *testing.T) {
	f := newAsyncFixture(t)
	f.allocations.err = errAsyncBoom
	f.reservations.releaseErr = errAsyncBoom
	f.reservations.releaseFailures = 2

	if err := f.orchestrator.Start(context.Background(), asyncAllocationRequest("saga-1", 7)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s := f.sagas.sagas["saga-1"]; s.Status != model.SagaCompensated {
		t.Fatalf("status = %s, want COMPENSATED", s.Status)
	}
}

func TestNotificationFailureKeepsCompletedSagaPendingAndRetries(t *testing.T) {
	f := newAsyncFixture(t)
	f.notifier.failures = 1
	f.notifier.err = errAsyncBoom

	if err := f.orchestrator.Start(context.Background(), asyncAllocationRequest("saga-1", 7)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	s := f.sagas.sagas["saga-1"]
	if s.Status != model.SagaCompleted || s.Notified {
		t.Fatalf("saga = %+v, want COMPLETED with notified=false", s)
	}
	if f.reservations.releaseCalls != 0 {
		t.Fatal("a notification failure must not compensate the allocation")
	}

	if err := f.orchestrator.RetryPendingNotifications(context.Background()); err != nil {
		t.Fatalf("RetryPendingNotifications: %v", err)
	}
	if s := f.sagas.sagas["saga-1"]; !s.Notified {
		t.Fatal("saga should be notified after retry")
	}
}

func TestStartIsIdempotentForExistingSaga(t *testing.T) {
	f := newAsyncFixture(t)
	req := asyncAllocationRequest("saga-1", 7)
	if err := f.orchestrator.Start(context.Background(), req); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := f.orchestrator.Start(context.Background(), req); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if f.reservations.reserveCalls != 1 {
		t.Fatalf("reserve calls = %d, want 1", f.reservations.reserveCalls)
	}
}

func TestStartRejectsInvalidRequest(t *testing.T) {
	f := newAsyncFixture(t)
	req := asyncAllocationRequest("saga-1", 7)
	req.Resource.RAMMB = 0
	if err := f.orchestrator.Start(context.Background(), req); err == nil {
		t.Fatal("invalid request should return error")
	}
	if len(f.sagas.sagas) != 0 {
		t.Fatal("invalid request must not create a saga")
	}
}

func TestNewSagaOrchestratorRejectsInvalidPolicy(t *testing.T) {
	f := newAsyncFixture(t)
	_, err := NewSagaOrchestrator(SagaDependencies{
		Sagas:        f.sagas,
		Metrics:      f.metrics,
		Reservations: f.reservations,
		Allocations:  f.allocations,
		Notifier:     f.notifier,
	}, SagaOptions{Compensation: CompensationPolicy{MaxAttempts: 0, InitialBackoff: time.Second, MaxBackoff: time.Second}})
	if err == nil {
		t.Fatal("zero max attempts should be rejected")
	}
}

func TestCompensationBackoffIsCapped(t *testing.T) {
	p := DefaultCompensationPolicy()
	if got := p.backoff(1); got != time.Second {
		t.Fatalf("backoff(1) = %s", got)
	}
	if got := p.backoff(10); got != 30*time.Second {
		t.Fatalf("backoff(10) = %s, want cap 30s", got)
	}
}
