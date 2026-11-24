package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

func TestRoleObserverReportsWinAndKnownPreviousLeader(t *testing.T) {
	transport := &electionTransportFake{}
	membership := electionMembershipFake{local: model.Node{ID: 1}, peers: []model.Node{{ID: 2}, {ID: 3}}}
	uc, err := NewBullyElectionUseCaseWithTimeout(membership, transport, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	var events []RoleEvent
	uc.SetRoleObserver(func(e RoleEvent) { events = append(events, e) })

	// Node 3 was the coordinator before the failure.
	if _, err := uc.HandleCoordinator(model.Node{ID: 3}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("follower role should not emit an event, got %+v", events)
	}

	// Nobody with higher priority answers: node 1 wins.
	if _, err := uc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !events[0].IsCoordinator || events[0].PreviousLeader != 3 {
		t.Fatalf("events = %+v, want one coordinator event with previous leader 3", events)
	}
}

func TestRoleObserverReportsLossWhenHigherNodeAnnounces(t *testing.T) {
	transport := &electionTransportFake{}
	membership := electionMembershipFake{local: model.Node{ID: 1}, peers: []model.Node{{ID: 2}}}
	uc, _ := NewBullyElectionUseCaseWithTimeout(membership, transport, time.Millisecond)
	if _, err := uc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	var events []RoleEvent
	uc.SetRoleObserver(func(e RoleEvent) { events = append(events, e) })
	if _, err := uc.HandleCoordinator(model.Node{ID: 2}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].IsCoordinator {
		t.Fatalf("events = %+v, want one loss event", events)
	}
}

func TestRoleObserverNotCalledWhenRoleDoesNotChange(t *testing.T) {
	transport := &electionTransportFake{}
	membership := electionMembershipFake{local: model.Node{ID: 1}, peers: []model.Node{{ID: 2}}}
	uc, _ := NewBullyElectionUseCaseWithTimeout(membership, transport, time.Millisecond)

	calls := 0
	uc.SetRoleObserver(func(RoleEvent) { calls++ })
	for i := 0; i < 3; i++ {
		if _, err := uc.HandleCoordinator(model.Node{ID: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatalf("observer called %d times for a follower that stays follower", calls)
	}
}
