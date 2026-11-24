package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

type fakeRequester struct {
	err      error
	calls    int
	resource model.Resource
}

func (f *fakeRequester) Request(_ context.Context, resource model.Resource) (model.SagaID, model.MessageID, error) {
	f.calls++
	f.resource = resource
	if f.err != nil {
		return "", "", f.err
	}
	return "saga-1", "msg-1", nil
}

func postAllocation(t *testing.T, requester AllocationRequester, body string) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := NewAllocationHandler(requester, time.Second)
	if err != nil {
		t.Fatalf("NewAllocationHandler: %v", err)
	}
	mux := http.NewServeMux()
	handler.Register(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/allocations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	return rec
}

const validAllocationBody = `{"resource":{"cpu":2,"ram":1024,"bandwidth":100}}`

func TestAllocationHandlerAcceptsAndReturnsIDs(t *testing.T) {
	requester := &fakeRequester{}
	rec := postAllocation(t, requester, validAllocationBody)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s, want 202", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"saga_id":"saga-1"`) || !strings.Contains(body, `"message_id":"msg-1"`) {
		t.Fatalf("body = %s", body)
	}
	if requester.resource.CPU != 2 || requester.resource.RAMMB != 1024 || requester.resource.BandwidthMbps != 100 {
		t.Fatalf("resource = %+v", requester.resource)
	}
}

func TestAllocationHandlerRejectsInvalidJSON(t *testing.T) {
	requester := &fakeRequester{}
	rec := postAllocation(t, requester, `{"resource":`)

	if rec.Code != http.StatusBadRequest || requester.calls != 0 {
		t.Fatalf("code=%d calls=%d, want 400 without publishing", rec.Code, requester.calls)
	}
}

func TestAllocationHandlerRejectsUnknownField(t *testing.T) {
	requester := &fakeRequester{}
	rec := postAllocation(t, requester, `{"resource":{"cpu":2,"ram":1024,"bandwidth":100},"force_fail":true}`)

	if rec.Code != http.StatusBadRequest || requester.calls != 0 {
		t.Fatalf("code=%d calls=%d, want 400 without publishing", rec.Code, requester.calls)
	}
}

func TestAllocationHandlerRejectsMissingOrInvalidResource(t *testing.T) {
	cases := map[string]string{
		"missing resource": `{}`,
		"zero cpu":         `{"resource":{"cpu":0,"ram":1024,"bandwidth":100}}`,
		"zero ram":         `{"resource":{"cpu":2,"ram":0,"bandwidth":100}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			requester := &fakeRequester{}
			rec := postAllocation(t, requester, body)
			if rec.Code != http.StatusBadRequest || requester.calls != 0 {
				t.Fatalf("code=%d calls=%d, want 400 without publishing", rec.Code, requester.calls)
			}
		})
	}
}

func TestAllocationHandlerReturnsServiceUnavailableOnPublishError(t *testing.T) {
	requester := &fakeRequester{err: errors.New("dial RabbitMQ: connection refused")}
	rec := postAllocation(t, requester, validAllocationBody)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "connection refused") {
		t.Fatalf("response leaks broker error: %s", rec.Body.String())
	}
}

func TestNewAllocationHandlerValidatesArguments(t *testing.T) {
	if _, err := NewAllocationHandler(nil, time.Second); err == nil {
		t.Fatal("nil requester should fail")
	}
	if _, err := NewAllocationHandler(&fakeRequester{}, 0); err == nil {
		t.Fatal("zero timeout should fail")
	}
}
