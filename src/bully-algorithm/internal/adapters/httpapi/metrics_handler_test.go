package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeReporter struct {
	err   error
	calls int
}

func (f *fakeReporter) Report(context.Context) error {
	f.calls++
	return f.err
}

func TestMetricsHandlerPublishesOnPost(t *testing.T) {
	reporter := &fakeReporter{}
	handler, err := NewMetricsHandler(reporter, time.Second)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"published"`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if reporter.calls != 1 {
		t.Fatalf("reporter calls = %d, want 1", reporter.calls)
	}
}

func TestMetricsHandlerReturnsServiceUnavailableOnPublishError(t *testing.T) {
	reporter := &fakeReporter{err: errors.New("dial RabbitMQ: connection refused")}
	handler, err := NewMetricsHandler(reporter, time.Second)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "connection refused") {
		t.Fatalf("response leaks broker error: %s", rec.Body.String())
	}
}

func TestMetricsHandlerRejectsOtherMethods(t *testing.T) {
	handler, err := NewMetricsHandler(&fakeReporter{}, time.Second)
	if err != nil {
		t.Fatalf("NewMetricsHandler: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code=%d, want 405", rec.Code)
	}
}

func TestNewMetricsHandlerValidatesArguments(t *testing.T) {
	if _, err := NewMetricsHandler(nil, time.Second); err == nil {
		t.Fatal("nil reporter should fail")
	}
	if _, err := NewMetricsHandler(&fakeReporter{}, 0); err == nil {
		t.Fatal("zero timeout should fail")
	}
}
