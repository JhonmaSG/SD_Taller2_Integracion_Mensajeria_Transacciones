// Package httpapi expone endpoints REST del nodo. Hoy solo incluye la
// publicación manual de métricas.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

// MetricsReporter publica una lectura de métricas en el tópico metrics.
// usecase.MetricsReporter cumple esta interfaz.
type MetricsReporter interface {
	Report(ctx context.Context) error
}

// MetricsHandler atiende POST /metrics y publica una lectura inmediata.
type MetricsHandler struct {
	reporter MetricsReporter
	timeout  time.Duration
}

// NewMetricsHandler valida las dependencias. timeout limita cada publicación.
func NewMetricsHandler(reporter MetricsReporter, timeout time.Duration) (*MetricsHandler, error) {
	if reporter == nil {
		return nil, errors.New("metrics reporter must not be nil")
	}
	if timeout <= 0 {
		return nil, errors.New("metrics publish timeout must be greater than zero")
	}
	return &MetricsHandler{reporter: reporter, timeout: timeout}, nil
}

// Routes devuelve el enrutador con las rutas del nodo. Go responde 405 a
// cualquier método distinto de POST en /metrics.
func (h *MetricsHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /metrics", h.publish)
	return mux
}

type publishResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (h *MetricsHandler) publish(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	if err := h.reporter.Report(ctx); err != nil {
		// El detalle del broker queda en los logs del nodo. La respuesta no lo expone.
		log.Printf("manual metrics publish failed: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, publishResponse{
			Status: "failed",
			Error:  "metrics not published, see node logs",
		})
		return
	}
	writeJSON(w, http.StatusOK, publishResponse{Status: "published"})
}

func writeJSON(w http.ResponseWriter, status int, body publishResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
