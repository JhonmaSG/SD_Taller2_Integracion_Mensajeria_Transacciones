package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

func TestAllocationRequesterPublishesRequestFromLocalNode(t *testing.T) {
	clock := &publishClockFake{}
	pub := &publisherFake{}
	requester, err := NewAllocationRequester(publishDeps(clock, pub))
	if err != nil {
		t.Fatalf("NewAllocationRequester: %v", err)
	}

	resource := model.Resource{CPU: 2, RAMMB: 1024, BandwidthMbps: 100}
	sagaID, messageID, err := requester.Request(context.Background(), resource)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	if len(pub.published) != 1 {
		t.Fatalf("published %d messages, want 1", len(pub.published))
	}
	request, ok := pub.published[0].(model.AllocationRequest)
	if !ok {
		t.Fatalf("published %T, want model.AllocationRequest", pub.published[0])
	}
	if request.SourceNode != 2 || request.LamportTS != 1 || request.Type != model.TypeAllocationRequest {
		t.Fatalf("request = %+v", request)
	}
	if request.Resource != resource {
		t.Fatalf("resource = %+v, want %+v", request.Resource, resource)
	}
	if request.SagaID != sagaID || request.MessageID != messageID {
		t.Fatalf("returned ids (%s, %s) do not match message (%s, %s)", sagaID, messageID, request.SagaID, request.MessageID)
	}
}

func TestAllocationRequesterRejectsInvalidResourceWithoutPublishing(t *testing.T) {
	pub := &publisherFake{}
	requester, err := NewAllocationRequester(publishDeps(&publishClockFake{}, pub))
	if err != nil {
		t.Fatalf("NewAllocationRequester: %v", err)
	}

	if _, _, err := requester.Request(context.Background(), model.Resource{CPU: 0, RAMMB: 1024}); err == nil {
		t.Fatal("zero cpu should be rejected")
	}
	if len(pub.published) != 0 {
		t.Fatalf("published %d messages for an invalid resource", len(pub.published))
	}
}

func TestAllocationRequesterWrapsPublisherError(t *testing.T) {
	pub := &publisherFake{err: errors.New("broker down")}
	requester, err := NewAllocationRequester(publishDeps(&publishClockFake{}, pub))
	if err != nil {
		t.Fatalf("NewAllocationRequester: %v", err)
	}

	_, _, err = requester.Request(context.Background(), model.Resource{CPU: 1, RAMMB: 512})
	if err == nil || !errors.Is(err, pub.err) {
		t.Fatalf("err = %v, want wrapped publisher error", err)
	}
}
