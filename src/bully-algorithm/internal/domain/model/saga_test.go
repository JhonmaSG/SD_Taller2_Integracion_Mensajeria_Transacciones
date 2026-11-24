package model

import (
	"testing"
	"time"
)

func newTestSaga(t *testing.T) Saga {
	t.Helper()
	s, err := NewSaga("saga-1", 1, Resource{CPU: 1, RAMMB: 512, BandwidthMbps: 10}, 7, time.Now())
	if err != nil {
		t.Fatalf("NewSaga: %v", err)
	}
	if s.Status != SagaInProgress {
		t.Fatalf("new saga status = %s, want IN_PROGRESS", s.Status)
	}
	return s
}

func TestNewSagaValidatesInput(t *testing.T) {
	res := Resource{CPU: 1, RAMMB: 512, BandwidthMbps: 10}
	if _, err := NewSaga("", 1, res, 1, time.Now()); err == nil {
		t.Fatal("missing saga id should fail")
	}
	if _, err := NewSaga("s", 0, res, 1, time.Now()); err == nil {
		t.Fatal("zero source node should fail")
	}
	if _, err := NewSaga("s", 1, Resource{}, 1, time.Now()); err == nil {
		t.Fatal("invalid resource should fail")
	}
	if _, err := NewSaga("s", 1, res, 0, time.Now()); err == nil {
		t.Fatal("zero lamport should fail")
	}
}

func TestSagaHappyPathTransitions(t *testing.T) {
	s := newTestSaga(t)
	at := time.Now()
	must(t, s.MarkReserved("res-1", at))
	must(t, s.Complete(at))
	if s.Status != SagaCompleted {
		t.Fatalf("status = %s, want COMPLETED", s.Status)
	}
	must(t, s.MarkNotified(at))
	if !s.Notified {
		t.Fatal("saga should be notified")
	}
}

func TestSagaRejectsIllegalTransitions(t *testing.T) {
	at := time.Now()

	t.Run("complete without reservation", func(t *testing.T) {
		s := newTestSaga(t)
		if err := s.Complete(at); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("double reservation", func(t *testing.T) {
		s := newTestSaga(t)
		must(t, s.MarkReserved("res-1", at))
		if err := s.MarkReserved("res-2", at); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("reject after reservation", func(t *testing.T) {
		s := newTestSaga(t)
		must(t, s.MarkReserved("res-1", at))
		if err := s.Reject(ReasonInsufficientResources, at); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("notify before complete", func(t *testing.T) {
		s := newTestSaga(t)
		if err := s.MarkNotified(at); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("notify twice", func(t *testing.T) {
		s := newTestSaga(t)
		must(t, s.MarkReserved("res-1", at))
		must(t, s.Complete(at))
		must(t, s.MarkNotified(at))
		if err := s.MarkNotified(at); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestSagaRejectBeforeReservationIsFailed(t *testing.T) {
	s := newTestSaga(t)
	must(t, s.Reject(ReasonInsufficientResources, time.Now()))
	if s.Status != SagaFailed || s.FailureReason != ReasonInsufficientResources {
		t.Fatalf("status=%s reason=%q", s.Status, s.FailureReason)
	}
}

func TestSagaCompensationFailureCanBeRetried(t *testing.T) {
	s := newTestSaga(t)
	at := time.Now()
	must(t, s.MarkReserved("res-1", at))
	must(t, s.RecordCompensationAttempt(at))
	must(t, s.MarkCompensationFailed("timeout", at))
	if s.Status != SagaCompensationFailed || s.CompensationAttempts != 1 {
		t.Fatalf("status=%s attempts=%d", s.Status, s.CompensationAttempts)
	}
	must(t, s.RecordCompensationAttempt(at))
	must(t, s.MarkCompensated(at))
	if s.Status != SagaCompensated || s.CompensationAttempts != 2 {
		t.Fatalf("status=%s attempts=%d", s.Status, s.CompensationAttempts)
	}
}
