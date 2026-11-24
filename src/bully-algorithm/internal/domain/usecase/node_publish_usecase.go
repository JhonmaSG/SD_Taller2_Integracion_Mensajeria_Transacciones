package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

// ErrCoordinatorSkipsMetrics indica que el reporte se omitió porque el nodo es
// coordinador. El coordinador consume la cola metrics y no publica sus propias
// métricas. No es un fallo de publicación.
var ErrCoordinatorSkipsMetrics = errors.New("coordinator does not publish its own metrics")

// NewRandomMessageID genera un message_id único para cada mensaje publicado.
func NewRandomMessageID() model.MessageID {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// rand.Read de crypto/rand no falla en sistemas soportados. Si falla, el
		// identificador deja de ser único, y eso debe detener el proceso.
		panic(fmt.Sprintf("generate message id: %v", err))
	}
	return model.MessageID(hex.EncodeToString(raw[:]))
}

// PublishDeps agrupa lo que necesitan los casos de uso de publicación del nodo.
type PublishDeps struct {
	NodeID    model.NodeID
	Clock     ports.LamportClock
	Publisher ports.MessagePublisher
	// NewID y Now son opcionales. Por defecto usan NewRandomMessageID y time.Now.
	NewID func() model.MessageID
	Now   func() time.Time
}

func (d PublishDeps) validate() error {
	if d.NodeID == 0 {
		return errors.New("node id must be greater than zero")
	}
	if d.Clock == nil {
		return errors.New("lamport clock must not be nil")
	}
	if d.Publisher == nil {
		return errors.New("message publisher must not be nil")
	}
	return nil
}

// MetricsReporter lee las métricas del host y las publica en el tópico metrics.
// Lo ejecutan los nodos seguidores. El coordinador no publica sus métricas
// porque consume la cola metrics.
type MetricsReporter struct {
	deps          PublishDeps
	sampler       ports.MachineMetricsReader
	isCoordinator func() bool
}

// NewMetricsReporter valida las dependencias y crea el reportador. isCoordinator
// indica si el nodo local es coordinador en este momento. Si es nil, el nodo
// nunca se considera coordinador.
func NewMetricsReporter(deps PublishDeps, sampler ports.MachineMetricsReader, isCoordinator func() bool) (*MetricsReporter, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	if sampler == nil {
		return nil, errors.New("machine metrics reader must not be nil")
	}
	if isCoordinator == nil {
		isCoordinator = func() bool { return false }
	}
	return &MetricsReporter{deps: withPublishDefaults(deps), sampler: sampler, isCoordinator: isCoordinator}, nil
}

// Report toma una lectura, avanza el reloj de Lamport y publica el reporte.
// Si el nodo es coordinador, devuelve ErrCoordinatorSkipsMetrics sin leer el
// sensor ni publicar.
func (r *MetricsReporter) Report(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	if r.isCoordinator() {
		return ErrCoordinatorSkipsMetrics
	}
	readings, err := r.sampler.ReadMetrics(ctx)
	if err != nil {
		return fmt.Errorf("read machine metrics: %w", err)
	}

	report := model.MetricReport{
		MessageID:     r.deps.NewID(),
		Type:          model.TypeMetricReport,
		NodeID:        r.deps.NodeID,
		LamportTS:     r.deps.Clock.Tick(),
		Timestamp:     r.deps.Now().UTC(),
		CPUTempC:      readings.CPUTempC,
		FreeRAMMB:     readings.FreeRAMMB,
		BandwidthMbps: readings.BandwidthMbps,
	}
	if err := r.deps.Publisher.Publish(ctx, report); err != nil {
		return fmt.Errorf("publish metrics: %w", err)
	}
	return nil
}

// LeadershipAnnouncer publica en el tópico failover el cambio de coordinador
// que el nodo local acaba de asumir.
type LeadershipAnnouncer struct {
	deps PublishDeps
}

// NewLeadershipAnnouncer valida las dependencias y crea el anunciador.
func NewLeadershipAnnouncer(deps PublishDeps) (*LeadershipAnnouncer, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	return &LeadershipAnnouncer{deps: withPublishDefaults(deps)}, nil
}

// AnnounceLeaderElected publica LEADER_ELECTED con el líder anterior y el nuevo
// líder, que es el nodo local.
func (a *LeadershipAnnouncer) AnnounceLeaderElected(ctx context.Context, previousLeader model.NodeID) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	event := model.FailoverEvent{
		MessageID:      a.deps.NewID(),
		Type:           model.TypeLeaderElected,
		PreviousLeader: previousLeader,
		NewLeader:      a.deps.NodeID,
		LamportTS:      a.deps.Clock.Tick(),
	}
	if err := a.deps.Publisher.Publish(ctx, event); err != nil {
		return fmt.Errorf("publish leader elected: %w", err)
	}
	return nil
}

// AllocationRequester publica en el tópico allocations una solicitud de
// asignación para el nodo local. El nodo local es el source_node, es decir, el
// nodo donde se reserva el recurso.
type AllocationRequester struct {
	deps PublishDeps
}

// NewAllocationRequester valida las dependencias y crea el publicador de solicitudes.
func NewAllocationRequester(deps PublishDeps) (*AllocationRequester, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	return &AllocationRequester{deps: withPublishDefaults(deps)}, nil
}

// Request valida el recurso, genera saga_id y message_id, avanza el reloj de
// Lamport y publica ALLOCATION_REQUEST. Devuelve cuando el broker confirma la
// publicación, sin esperar a que la saga termine.
func (a *AllocationRequester) Request(ctx context.Context, resource model.Resource) (model.SagaID, model.MessageID, error) {
	if ctx == nil {
		return "", "", errors.New("context must not be nil")
	}
	if err := resource.Validate(); err != nil {
		return "", "", fmt.Errorf("invalid resource: %w", err)
	}

	sagaID := model.SagaID(a.deps.NewID())
	request := model.AllocationRequest{
		MessageID:  a.deps.NewID(),
		Type:       model.TypeAllocationRequest,
		SourceNode: a.deps.NodeID,
		Resource:   resource,
		LamportTS:  a.deps.Clock.Tick(),
		SagaID:     sagaID,
	}
	if err := request.Validate(); err != nil {
		return "", "", fmt.Errorf("invalid allocation request: %w", err)
	}
	if err := a.deps.Publisher.Publish(ctx, request); err != nil {
		return "", "", fmt.Errorf("publish allocation request: %w", err)
	}
	return sagaID, request.MessageID, nil
}

func withPublishDefaults(deps PublishDeps) PublishDeps {
	if deps.NewID == nil {
		deps.NewID = NewRandomMessageID
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return deps
}
