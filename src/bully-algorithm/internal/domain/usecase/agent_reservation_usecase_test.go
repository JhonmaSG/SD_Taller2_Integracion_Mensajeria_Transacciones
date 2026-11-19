package usecase

import (
	"errors"
	"testing"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

func newAgentLedger(t *testing.T) *AgentReservationUseCase {
	t.Helper()
	u, err := NewAgentReservationUseCase(2, model.Resource{CPU: 4, RAMMB: 4096, BandwidthMbps: 1000})
	if err != nil {
		t.Fatalf("NewAgentReservationUseCase: %v", err)
	}
	return u
}

func ledgerResource(ram uint64) model.Resource {
	return model.Resource{CPU: 1, RAMMB: ram, BandwidthMbps: 10}
}

func TestAgentReserveIsIdempotentPerSaga(t *testing.T) {
	u := newAgentLedger(t)
	first, err := u.Reserve("saga-1", 2, ledgerResource(1000))
	if err != nil || !first.Accepted || first.ReservationID != "res-saga-1" {
		t.Fatalf("first reserve = %+v, %v", first, err)
	}
	again, err := u.Reserve("saga-1", 2, ledgerResource(1000))
	if err != nil || !again.Accepted || again.ReservationID != first.ReservationID {
		t.Fatalf("repeated reserve = %+v, %v; want same reservation", again, err)
	}
	// La repetición no debe descontar recursos otra vez: aún caben 3000 MB.
	big, _ := u.Reserve("saga-2", 2, ledgerResource(3000))
	if !big.Accepted {
		t.Fatalf("reserve after repeat = %+v, want accepted (RAM not double-counted)", big)
	}
}

func TestAgentRejectsWhenCapacityExceeded(t *testing.T) {
	u := newAgentLedger(t)
	must(t, mustOutcome(u.Reserve("saga-1", 2, ledgerResource(3000))))

	out, err := u.Reserve("saga-2", 2, ledgerResource(2000))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if out.Accepted || out.Reason != ReasonInsufficientCapacity {
		t.Fatalf("outcome = %+v, want INSUFFICIENT_CAPACITY", out)
	}
}

func TestAgentRejectsReservationForOtherNode(t *testing.T) {
	u := newAgentLedger(t)
	out, err := u.Reserve("saga-1", 7, ledgerResource(100))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if out.Accepted || out.Reason != ReasonWrongNode {
		t.Fatalf("outcome = %+v, want WRONG_NODE", out)
	}
}

func TestAgentReleaseFreesCapacityAndIsIdempotent(t *testing.T) {
	u := newAgentLedger(t)
	must(t, mustOutcome(u.Reserve("saga-1", 2, ledgerResource(3000))))

	if err := u.Release("saga-1", "res-saga-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := u.Release("saga-1", "res-saga-1"); err != nil {
		t.Fatalf("second Release should be a no-op: %v", err)
	}
	if err := u.Release("never-existed", ""); err != nil {
		t.Fatalf("Release of unknown saga should succeed: %v", err)
	}
	out, _ := u.Reserve("saga-2", 2, ledgerResource(3000))
	if !out.Accepted {
		t.Fatalf("reserve after release = %+v, want accepted", out)
	}
}

func TestAgentReleaseRejectsMismatchedReservation(t *testing.T) {
	u := newAgentLedger(t)
	must(t, mustOutcome(u.Reserve("saga-1", 2, ledgerResource(100))))
	if err := u.Release("saga-1", "res-other"); err == nil {
		t.Fatal("release with another reservation id should fail")
	}
}

func TestAgentCannotReleaseConfirmedAllocation(t *testing.T) {
	u := newAgentLedger(t)
	must(t, mustOutcome(u.Reserve("saga-1", 2, ledgerResource(100))))
	found, err := u.Confirm("saga-1")
	if err != nil || !found {
		t.Fatalf("Confirm = %v, %v", found, err)
	}
	if err := u.Release("saga-1", ""); err == nil {
		t.Fatal("confirmed allocation must not be released")
	}
}

func TestAgentConfirmUnknownSagaReportsNotHeld(t *testing.T) {
	u := newAgentLedger(t)
	found, err := u.Confirm("saga-unknown")
	if err != nil || found {
		t.Fatalf("Confirm unknown = %v, %v; want false, nil", found, err)
	}
}

func TestAgentRejectsInvalidInput(t *testing.T) {
	u := newAgentLedger(t)
	if _, err := u.Reserve("", 2, ledgerResource(100)); err == nil {
		t.Fatal("empty saga id should fail")
	}
	if _, err := u.Reserve("saga-1", 2, model.Resource{}); err == nil {
		t.Fatal("invalid resource should fail")
	}
	if _, err := NewAgentReservationUseCase(0, model.Resource{CPU: 1, RAMMB: 1}); err == nil {
		t.Fatal("zero local node should fail")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustOutcome(out ReserveOutcome, err error) error {
	if err != nil {
		return err
	}
	if !out.Accepted {
		return errors.New("reserve rejected in test setup")
	}
	return nil
}
