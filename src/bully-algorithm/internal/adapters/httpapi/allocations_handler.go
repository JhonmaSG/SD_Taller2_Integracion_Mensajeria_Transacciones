package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

// AllocationRequester publica una solicitud de asignación para el nodo local.
// usecase.AllocationRequester cumple esta interfaz.
type AllocationRequester interface {
	Request(ctx context.Context, resource model.Resource) (model.SagaID, model.MessageID, error)
}

// AllocationHandler atiende POST /allocations. Publica la solicitud y responde
// 202 sin esperar a que la saga termine.
type AllocationHandler struct {
	requester AllocationRequester
	timeout   time.Duration
}

// NewAllocationHandler valida las dependencias. timeout limita la publicación.
func NewAllocationHandler(requester AllocationRequester, timeout time.Duration) (*AllocationHandler, error) {
	if requester == nil {
		return nil, errors.New("allocation requester must not be nil")
	}
	if timeout <= 0 {
		return nil, errors.New("allocation publish timeout must be greater than zero")
	}
	return &AllocationHandler{requester: requester, timeout: timeout}, nil
}

// Register agrega POST /allocations al enrutador del nodo.
func (h *AllocationHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /allocations", h.allocate)
}

// allocationBody usa los mismos nombres de campos que el payload de RabbitMQ.
type allocationBody struct {
	Resource *resourceBody `json:"resource"`
}

type resourceBody struct {
	CPU           float64 `json:"cpu"`
	RAMMB         uint64  `json:"ram"`
	BandwidthMbps float64 `json:"bandwidth"`
}

type allocationResponse struct {
	Status    string `json:"status"`
	SagaID    string `json:"saga_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

const maxAllocationBodyBytes = 4 << 10

func (h *AllocationHandler) allocate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAllocationBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	var body allocationBody
	if err := decoder.Decode(&body); err != nil {
		writeAllocationJSON(w, http.StatusBadRequest, allocationResponse{Status: "rejected", Error: "invalid JSON body"})
		return
	}
	if body.Resource == nil {
		writeAllocationJSON(w, http.StatusBadRequest, allocationResponse{Status: "rejected", Error: "resource is required"})
		return
	}
	resource := model.Resource{
		CPU:           body.Resource.CPU,
		RAMMB:         body.Resource.RAMMB,
		BandwidthMbps: body.Resource.BandwidthMbps,
	}
	if err := resource.Validate(); err != nil {
		writeAllocationJSON(w, http.StatusBadRequest, allocationResponse{Status: "rejected", Error: err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	sagaID, messageID, err := h.requester.Request(ctx, resource)
	if err != nil {
		// El detalle del broker queda en los logs del nodo.
		log.Printf("allocation request not published: %v", err)
		writeAllocationJSON(w, http.StatusServiceUnavailable, allocationResponse{
			Status: "failed",
			Error:  "allocation request not published, see node logs",
		})
		return
	}
	writeAllocationJSON(w, http.StatusAccepted, allocationResponse{
		Status:    "accepted",
		SagaID:    string(sagaID),
		MessageID: string(messageID),
	})
}

func writeAllocationJSON(w http.ResponseWriter, status int, body allocationResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
