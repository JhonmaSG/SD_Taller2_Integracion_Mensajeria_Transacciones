package persistence

import (
	"testing"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

var asyncTestTime = time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC)

func asyncTestSaga() model.Saga {
	return model.Saga{
		ID:         "saga-9921",
		SourceNode: 1,
		Resource:   model.Resource{CPU: 2, RAMMB: 1024, BandwidthMbps: 100},
		LamportTS:  48,
		Status:     model.SagaInProgress,
		CreatedAt:  asyncTestTime,
		UpdatedAt:  asyncTestTime,
	}
}

func TestParseAsyncNodeKey(t *testing.T) {
	if id, err := parseAsyncNodeKey("node-3"); err != nil || id != 3 {
		t.Fatalf("parse node-3 = %d, %v", id, err)
	}
	for _, bad := range []string{"node_3", "node-0", "node-x", "3", ""} {
		if _, err := parseAsyncNodeKey(bad); err == nil {
			t.Fatalf("parse %q should fail", bad)
		}
	}
}

func TestAsyncStepsForRejectedBeforeReservation(t *testing.T) {
	s := asyncTestSaga()
	if err := s.Reject(model.ReasonInsufficientResources, asyncTestTime); err != nil {
		t.Fatal(err)
	}
	steps := asyncStepsFor(s)
	assertStep(t, steps[0], asyncStepFailed, false)
	assertStep(t, steps[1], asyncStepPending, false)
	assertStep(t, steps[2], asyncStepPending, false)
	if steps[0].ReservationID != nil {
		t.Fatal("rejected saga must not report a reservation")
	}
}

func TestAsyncStepsForReplicationFailureCompensated(t *testing.T) {
	s := asyncTestSaga()
	must(t, s.MarkReserved("res-01", asyncTestTime))
	must(t, s.BeginCompensation(model.StepReplicateAllocation, asyncTestTime))
	must(t, s.RecordCompensationAttempt(asyncTestTime))
	must(t, s.MarkCompensated(asyncTestTime))

	steps := asyncStepsFor(s)
	assertStep(t, steps[0], asyncStepCompleted, true)
	if steps[0].ReservationID == nil || *steps[0].ReservationID != "res-01" {
		t.Fatalf("reservation_id = %v, want res-01", steps[0].ReservationID)
	}
	assertStep(t, steps[1], asyncStepFailed, false)
	assertStep(t, steps[2], asyncStepPending, false)
}

func TestAsyncStepsForCompletedSaga(t *testing.T) {
	s := asyncTestSaga()
	must(t, s.MarkReserved("res-01", asyncTestTime))
	must(t, s.Complete(asyncTestTime))

	steps := asyncStepsFor(s)
	for i := range steps {
		assertStep(t, steps[i], asyncStepCompleted, false)
	}
}

func TestAsyncSagaDocRoundTripKeepsDomainState(t *testing.T) {
	s := asyncTestSaga()
	must(t, s.MarkReserved("res-01", asyncTestTime))
	must(t, s.BeginCompensation(model.StepReplicateAllocation, asyncTestTime))
	must(t, s.RecordCompensationAttempt(asyncTestTime))
	must(t, s.RecordCompensationError("timeout releasing reservation", asyncTestTime))
	must(t, s.MarkCompensationFailed("release failed after 5 attempts", asyncTestTime))
	s.Version = 4

	back, err := asyncSagaFromDoc(asyncSagaToDoc(s))
	if err != nil {
		t.Fatalf("asyncSagaFromDoc: %v", err)
	}
	if back.ID != s.ID || back.SourceNode != s.SourceNode || back.LamportTS != s.LamportTS {
		t.Fatalf("identity mismatch: %+v", back)
	}
	if back.Resource != s.Resource {
		t.Fatalf("resource = %+v, want %+v", back.Resource, s.Resource)
	}
	if back.Status != s.Status || back.FailureReason != s.FailureReason {
		t.Fatalf("status/reason = %s/%q", back.Status, back.FailureReason)
	}
	if back.ReservationID != "res-01" || back.FailedStep != model.StepReplicateAllocation {
		t.Fatalf("reservation=%q failedStep=%d", back.ReservationID, back.FailedStep)
	}
	if !back.CompensationRequired || back.CompensationAttempts != 1 || back.CompensationLastError != "timeout releasing reservation" {
		t.Fatalf("compensation = %+v", back)
	}
	if back.Version != 4 {
		t.Fatalf("version = %d, want 4", back.Version)
	}
}

func TestAsyncSagaDocNotificationFieldsRoundTrip(t *testing.T) {
	s := asyncTestSaga()
	must(t, s.MarkReserved("res-01", asyncTestTime))
	must(t, s.Complete(asyncTestTime))
	must(t, s.RecordNotificationFailure("agent unreachable", asyncTestTime))

	back, err := asyncSagaFromDoc(asyncSagaToDoc(s))
	if err != nil {
		t.Fatal(err)
	}
	if back.Notified || back.NotificationRetries != 1 || back.NotificationLastError != "agent unreachable" {
		t.Fatalf("notification = %+v", back)
	}
}

func assertStep(t *testing.T, step asyncStepDoc, status string, compensated bool) {
	t.Helper()
	if step.Status != status {
		t.Fatalf("step %d status = %s, want %s", step.Step, step.Status, status)
	}
	if step.Compensated != compensated {
		t.Fatalf("step %d compensated = %v, want %v", step.Step, step.Compensated, compensated)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
