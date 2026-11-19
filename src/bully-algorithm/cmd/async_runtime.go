package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/adapters/config"
	grpcadapter "github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/adapters/grpc"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/adapters/persistence"
	rabbitmqadapter "github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/adapters/rabbitmq"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/adapters/system"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/usecase"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

const (
	brokerDialTimeout      = 5 * time.Second
	brokerConfirmTimeout   = 5 * time.Second
	consumerDrainInterval  = 200 * time.Millisecond
	failoverPublishTimeout = 30 * time.Second
	storeCloseTimeout      = 5 * time.Second
)

// syncClock es el reloj de Lamport del nodo. Lo usan el consumidor, los
// publicadores y los reportes de métricas desde goroutines distintas.
type syncClock struct {
	mu    sync.Mutex
	clock model.LamportClock
}

var _ ports.LamportClock = (*syncClock)(nil)

func newSyncClock() *syncClock {
	return &syncClock{clock: model.NewLamportClock(0)}
}

func (c *syncClock) Tick() model.LamportTime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clock.Tick()
}

func (c *syncClock) Observe(remote model.LamportTime) model.LamportTime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clock.Observe(remote)
}

// asyncRuntime conecta el flujo asíncrono con el proceso del nodo:
//   - Todos los nodos publican métricas periódicamente.
//   - Al ser elegido coordinador, el nodo publica LEADER_ELECTED y consume las colas.
//   - Al dejar de ser coordinador, cancela el consumo y devuelve las entregas pendientes.
//
// Si RABBITMQ_URL no está definida, el flujo queda desactivado y el nodo sigue
// funcionando para Bully y gRPC.
type asyncRuntime struct {
	ctx        context.Context
	cfg        config.Config
	membership ports.MembershipPort
	clock      *syncClock
	agents     *grpcadapter.AgentClient

	publisher *rabbitmqadapter.Publisher
	announcer *usecase.LeadershipAnnouncer
	reporter  *usecase.MetricsReporter

	mu              sync.Mutex
	store           *persistence.AsyncStore
	orchestrator    *usecase.SagaOrchestrator
	wantCoordinator bool
	stopConsumer    context.CancelFunc
}

func newAsyncRuntime(
	ctx context.Context,
	cfg config.Config,
	membership ports.MembershipPort,
	agents *grpcadapter.AgentClient,
) (*asyncRuntime, error) {
	runtime := &asyncRuntime{
		ctx:        ctx,
		cfg:        cfg,
		membership: membership,
		clock:      newSyncClock(),
		agents:     agents,
	}
	if cfg.RabbitMQURL == "" {
		log.Printf("RABBITMQ_URL not set: asynchronous flow disabled on this node")
		return runtime, nil
	}

	publisher, err := rabbitmqadapter.NewPublisher(rabbitmqadapter.PublisherConfig{
		URL:            cfg.RabbitMQURL,
		Topology:       rabbitmqadapter.Topology{},
		DialTimeout:    brokerDialTimeout,
		ConfirmTimeout: brokerConfirmTimeout,
		Retry:          rabbitmqadapter.DefaultRetryPolicy(),
	})
	if err != nil {
		return nil, err
	}
	deps := usecase.PublishDeps{
		NodeID:    membership.LocalNode().ID,
		Clock:     runtime.clock,
		Publisher: publisher,
	}
	announcer, err := usecase.NewLeadershipAnnouncer(deps)
	if err != nil {
		return nil, err
	}
	reporter, err := usecase.NewMetricsReporter(deps, system.NewMachineMetricsSampler())
	if err != nil {
		return nil, err
	}
	runtime.publisher = publisher
	runtime.announcer = announcer
	runtime.reporter = reporter
	return runtime, nil
}

func (r *asyncRuntime) enabled() bool {
	return r.publisher != nil
}

// start inicia la publicación periódica de métricas y la conexión a MongoDB.
func (r *asyncRuntime) start() {
	if !r.enabled() {
		return
	}
	go r.reportMetrics()
	go r.connectStore()
}

// onRole recibe los cambios de rol de Bully. Debe ser rápido: el trabajo de
// red corre en goroutines.
func (r *asyncRuntime) onRole(event usecase.RoleEvent) {
	if !r.enabled() {
		return
	}

	r.mu.Lock()
	r.wantCoordinator = event.IsCoordinator
	if event.IsCoordinator {
		r.startConsumerLocked()
	} else {
		r.stopConsumerLocked()
		log.Printf("no longer coordinator: consumption stopped")
	}
	r.mu.Unlock()

	if event.IsCoordinator && event.PreviousLeader != 0 {
		go r.announceLeader(event.PreviousLeader)
	}
}

// shutdown detiene el consumo, cierra el publicador y el almacén.
func (r *asyncRuntime) shutdown() {
	if !r.enabled() {
		return
	}

	r.mu.Lock()
	r.stopConsumerLocked()
	store := r.store
	r.store = nil
	r.mu.Unlock()

	if err := r.publisher.Close(); err != nil {
		log.Printf("close RabbitMQ publisher: %v", err)
	}
	if store != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), storeCloseTimeout)
		defer cancel()
		if err := store.Close(closeCtx); err != nil {
			log.Printf("close async store: %v", err)
		}
	}
}

func (r *asyncRuntime) reportMetrics() {
	ticker := time.NewTicker(r.cfg.MetricsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			// El reporte expira antes del siguiente periodo para no acumular intentos.
			reportCtx, cancel := context.WithTimeout(r.ctx, r.cfg.MetricsInterval)
			if err := r.reporter.Report(reportCtx); err != nil {
				log.Printf("metrics report not published: %v", err)
			}
			cancel()
		}
	}
}

func (r *asyncRuntime) announceLeader(previous model.NodeID) {
	ctx, cancel := context.WithTimeout(r.ctx, failoverPublishTimeout)
	defer cancel()
	if err := r.announcer.AnnounceLeaderElected(ctx, previous); err != nil {
		log.Printf("LEADER_ELECTED not published (previous=%d): %v", previous, err)
	}
}

// connectStore conecta a MongoDB con reintentos. Mientras no conecta, el nodo
// sigue participando en Bully y en gRPC.
func (r *asyncRuntime) connectStore() {
	for {
		connectCtx, cancel := context.WithTimeout(r.ctx, mongoStartupTimeout)
		store, err := persistence.NewAsyncStore(connectCtx, r.cfg.DatabaseURI, r.cfg.DatabaseName)
		cancel()
		if err == nil {
			r.attachStore(store)
			return
		}
		log.Printf("async store unavailable, retrying in %s: %v", mongoRetryInterval, err)
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(mongoRetryInterval):
		}
	}
}

func (r *asyncRuntime) attachStore(store *persistence.AsyncStore) {
	orchestrator, err := usecase.NewSagaOrchestrator(usecase.SagaDependencies{
		Sagas:        store,
		Metrics:      store,
		Reservations: r.agents,
		Allocations:  store,
		Notifier:     r.agents,
	}, usecase.SagaOptions{})
	if err != nil {
		log.Printf("saga orchestrator unavailable: %v", err)
		closeStore(store)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.store = store
	r.orchestrator = orchestrator
	log.Printf("async store ready, database=%s", r.cfg.DatabaseName)
	if r.wantCoordinator {
		r.startConsumerLocked()
	}
}

// startConsumerLocked inicia el consumo si el nodo es coordinador y el almacén
// está listo. Debe llamarse con r.mu tomado.
func (r *asyncRuntime) startConsumerLocked() {
	if r.stopConsumer != nil || r.store == nil {
		return
	}

	ctx, cancel := context.WithCancel(r.ctx)
	consumer, err := rabbitmqadapter.NewConsumer(rabbitmqadapter.ConsumerConfig{
		URL:           r.cfg.RabbitMQURL,
		Topology:      rabbitmqadapter.Topology{},
		DialTimeout:   brokerDialTimeout,
		Prefetch:      r.cfg.ConsumerPrefetch,
		DrainInterval: consumerDrainInterval,
		Reconnect:     rabbitmqadapter.DefaultRetryPolicy(),
	}, r.processorFactory(r.store, r.orchestrator))
	if err != nil {
		cancel()
		log.Printf("consumer not started: %v", err)
		return
	}

	r.stopConsumer = cancel
	log.Printf("coordinator: consuming metrics, allocations and failover")
	go func() {
		if err := consumer.Run(ctx); err != nil {
			log.Printf("consumer stopped: %v", err)
		}
	}()
}

// stopConsumerLocked cancela el consumo. Debe llamarse con r.mu tomado.
func (r *asyncRuntime) stopConsumerLocked() {
	if r.stopConsumer != nil {
		r.stopConsumer()
		r.stopConsumer = nil
	}
}

// processorFactory crea un procesador nuevo con su buffer de orden. Cada vez que
// el nodo vuelve a ser coordinador se usa un buffer limpio.
func (r *asyncRuntime) processorFactory(
	store *persistence.AsyncStore,
	orchestrator *usecase.SagaOrchestrator,
) func() (rabbitmqadapter.MessageProcessor, error) {
	return func() (rabbitmqadapter.MessageProcessor, error) {
		buffer, err := model.NewCausalBuffer[model.Message](r.cfg.CausalWindow)
		if err != nil {
			return nil, err
		}
		processor, err := usecase.NewEventProcessor(usecase.EventProcessorDeps{
			Clock:     r.clock,
			Buffer:    buffer,
			Processed: store,
			Metrics:   store,
			Failovers: store,
			Sagas:     orchestrator,
		})
		if err != nil {
			return nil, err
		}
		return processor, nil
	}
}

func closeStore(store *persistence.AsyncStore) {
	closeCtx, cancel := context.WithTimeout(context.Background(), storeCloseTimeout)
	defer cancel()
	if err := store.Close(closeCtx); err != nil {
		log.Printf("close async store: %v", err)
	}
}
