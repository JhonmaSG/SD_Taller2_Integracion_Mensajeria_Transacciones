package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/adapters/config"
	grpcadapter "github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/adapters/grpc"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/adapters/persistence"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/adapters/system"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/usecase"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

const (
	mongoStartupTimeout = 10 * time.Second
	mongoCloseTimeout   = 5 * time.Second
	// mongoRetryInterval es la pausa entre intentos de conexión a MongoDB
	// mientras el proceso sigue participando en Bully.
	mongoRetryInterval = 5 * time.Second
	// capacityReadTimeout limita la lectura de capacidad del host al arrancar.
	capacityReadTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Printf("node stopped with error: %v", err)
		os.Exit(1)
	}
}

func run() error {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC)

	// El archivo es opcional. Las variables de entorno ya definidas siempre
	// tienen prioridad sobre los valores del archivo.
	if err := config.LoadDotEnvFile(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	membership, err := config.NewStaticMembership(cfg.LocalNode, cfg.Peers)
	if err != nil {
		return fmt.Errorf("create membership: %w", err)
	}

	// runtimeContext controla todas las goroutines de fondo del nodo.
	runtimeContext, stopRuntime := context.WithCancel(context.Background())
	defer stopRuntime()

	transport := grpcadapter.NewElectionTransport()
	election, err := usecase.NewBullyElectionUseCaseWithTimeout(
		membership,
		transport,
		cfg.ElectionTimeout,
	)
	if err != nil {
		return fmt.Errorf("create election use case: %w", err)
	}

	heartbeat, err := usecase.NewProcessHeartbeatUseCase(
		membership,
		transport,
		election,
		cfg.HeartbeatInterval,
		cfg.ElectionTimeout,
	)
	if err != nil {
		return fmt.Errorf("create heartbeat use case: %w", err)
	}

	electionServer, err := grpcadapter.NewElectionServer(election, heartbeat)
	if err != nil {
		return fmt.Errorf("create gRPC server adapter: %w", err)
	}

	// El agente reserva recursos con la capacidad total del host. Esta capacidad
	// se fija al arrancar.
	capacity, err := readAgentCapacity()
	if err != nil {
		return err
	}
	ledger, err := usecase.NewAgentReservationUseCase(cfg.LocalNode.ID, capacity)
	if err != nil {
		return fmt.Errorf("create agent reservation ledger: %w", err)
	}
	agentServer, err := grpcadapter.NewAgentServer(ledger)
	if err != nil {
		return fmt.Errorf("create agent gRPC adapter: %w", err)
	}
	agentClient, err := grpcadapter.NewAgentClient(membership, cfg.ElectionTimeout)
	if err != nil {
		return fmt.Errorf("create agent client: %w", err)
	}

	async, err := newAsyncRuntime(runtimeContext, cfg, membership, agentClient)
	if err != nil {
		return fmt.Errorf("create asynchronous runtime: %w", err)
	}
	// El observador se registra antes de la primera elección para no perder el
	// cambio de rol inicial.
	election.SetRoleObserver(async.onRole)

	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddress, err)
	}
	defer listener.Close()

	grpcServer := grpc.NewServer()
	electionServer.Register(grpcServer)
	agentServer.Register(grpcServer)
	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("gRPC server listening on %s", cfg.ListenAddress)
		serverErrors <- grpcServer.Serve(listener)
	}()

	if err := startElection(election, cfg.ElectionTimeout); err != nil {
		return err
	}

	// El monitor de heartbeats detecta la caída del coordinador y dispara una
	// nueva elección sin depender de que el proceso reciba tráfico entrante.
	go func() {
		if err := heartbeat.Run(runtimeContext); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("heartbeat monitor stopped: %v", err)
		}
	}()

	// MongoDB pertenece al plano de datos. La conexión se reintenta en segundo
	// plano: si la base no está disponible al arrancar, el nodo sigue en Bully
	// y se conecta en cuanto MongoDB responda.
	persist := &persistenceLink{}
	go persist.connect(runtimeContext, cfg, membership, election, transport, electionServer)

	async.start()

	log.Printf(
		"node %d started with %d peer(s), MongoDB database=%s collection=%s heartbeat=%s capacity=cpu:%.0f ram_mb:%d bandwidth_mbps:%.0f",
		cfg.LocalNode.ID,
		len(cfg.Peers),
		cfg.DatabaseName,
		cfg.NodeCollection,
		cfg.HeartbeatInterval,
		capacity.CPU,
		capacity.RAMMB,
		capacity.BandwidthMbps,
	)

	shutdownErr := waitForShutdown(grpcServer, serverErrors, stopRuntime)
	async.shutdown()
	persist.shutdown()
	return shutdownErr
}

// readAgentCapacity convierte la capacidad física del host a la unidad del dominio
// (CPU en núcleos, RAM en MB, ancho de banda en Mbps).
func readAgentCapacity() (model.Resource, error) {
	ctx, cancel := context.WithTimeout(context.Background(), capacityReadTimeout)
	defer cancel()

	machine, err := system.NewMachineCapacityReader().Read(ctx)
	if err != nil {
		return model.Resource{}, fmt.Errorf("read machine capacity: %w", err)
	}
	if machine.BandwidthMbps == 0 {
		log.Printf("link bandwidth unknown: reservations that request bandwidth will be rejected")
	}
	return model.Resource{
		CPU:           float64(machine.CPUCores),
		RAMMB:         uint64(machine.MemoryGB) * 1024,
		BandwidthMbps: float64(machine.BandwidthMbps),
	}, nil
}

// persistenceLink agrupa la conexión a MongoDB y los casos de uso que dependen
// de ella. Se crea antes de conectar y se completa cuando la base responde.
type persistenceLink struct {
	mu           sync.Mutex
	repository   *persistence.MongoNodeDocumentRepository
	registration *usecase.RegisterNodeUseCase
	closed       bool
}

// connect intenta conectar a MongoDB hasta que lo consigue o el contexto se
// cancela. Cada intento espera mongoRetryInterval después del fallo anterior.
func (p *persistenceLink) connect(
	ctx context.Context,
	cfg config.Config,
	membership ports.MembershipPort,
	election *usecase.BullyElectionUseCase,
	transport ports.NodeRegistrationTransport,
	electionServer *grpcadapter.ElectionServer,
) {
	for {
		repository := initializeRepository(cfg, election)
		if repository != nil {
			p.attach(ctx, cfg, membership, election, repository, transport, electionServer)
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(mongoRetryInterval):
		}
	}
}

// attach conecta los casos de uso de registro y de vigilancia de followers. Los
// casos de uso se conectan antes del primer registro: si ese registro falla por
// un problema transitorio, el nodo sigue aceptando RegisterNode y el bucle
// periódico reintenta.
func (p *persistenceLink) attach(
	ctx context.Context,
	cfg config.Config,
	membership ports.MembershipPort,
	election *usecase.BullyElectionUseCase,
	repository *persistence.MongoNodeDocumentRepository,
	transport ports.NodeRegistrationTransport,
	electionServer *grpcadapter.ElectionServer,
) {
	clusterMetadata, metadataErr := usecase.NewClusterMetadataUseCase(membership, election, repository)
	if metadataErr != nil {
		log.Printf("cluster metadata unavailable: %v", metadataErr)
	} else {
		metadataContext, cancelMetadata := context.WithTimeout(ctx, mongoStartupTimeout)
		if err := clusterMetadata.SyncOnce(metadataContext); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("cluster metadata sync failed, retrying: %v", err)
		}
		cancelMetadata()
		go func() {
			if err := clusterMetadata.Run(ctx, cfg.HeartbeatInterval, cfg.ElectionTimeout); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("cluster metadata monitor stopped: %v", err)
			}
		}()
	}

	registerNode, err := usecase.NewRegisterNodeUseCase(
		membership,
		election,
		repository,
		system.NewMachineCapacityReader(),
		transport,
	)
	if err != nil {
		log.Printf("node registration unavailable: %v", err)
		closeRepository(repository)
		return
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		closeRepository(repository)
		return
	}
	p.repository = repository
	p.registration = registerNode
	p.mu.Unlock()

	electionServer.SetRegistrationUseCase(registerNode)

	registerContext, cancelRegister := context.WithTimeout(ctx, mongoStartupTimeout)
	doc, firstErr := registerNode.Register(registerContext)
	cancelRegister()
	switch {
	case firstErr != nil:
		log.Printf("node document registration failed, retrying every %s: %v", cfg.HeartbeatInterval, firstErr)
	case doc.ID != 0:
		log.Printf("machine capacity registered cpu_cores=%d memory_gb=%d bandwidth_mbps=%d", doc.Resources.CPUCores, doc.Resources.MemoryGB, doc.Resources.BandwidthMbps)
	}

	// Solo el coordinador vigila a los followers. El umbral usa TIMEOUT_MS:
	// un follower sin heartbeats durante ese tiempo se marca OFFLINE.
	liveness, err := usecase.NewFollowerLivenessUseCase(membership, election, registerNode, cfg.ElectionTimeout)
	if err != nil {
		log.Printf("follower liveness unavailable: %v", err)
	} else {
		electionServer.SetFollowerLiveness(liveness)
		go func() {
			if err := liveness.Run(ctx, cfg.HeartbeatInterval, cfg.ElectionTimeout); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("follower liveness monitor stopped: %v", err)
			}
		}()
	}

	go func() {
		if err := registerNode.Run(ctx, cfg.HeartbeatInterval, cfg.ElectionTimeout); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("node document role monitor stopped: %v", err)
		}
	}()
}

// shutdown se ejecuta al terminar el proceso. Marca OFFLINE al nodo si era
// coordinador y cierra la conexión. Para followers, MarkOffline no escribe.
func (p *persistenceLink) shutdown() {
	p.mu.Lock()
	p.closed = true
	registration, repository := p.registration, p.repository
	p.mu.Unlock()

	if registration != nil {
		offlineContext, cancelOffline := context.WithTimeout(context.Background(), mongoCloseTimeout)
		if err := registration.MarkOffline(offlineContext); err != nil {
			log.Printf("mark node offline: %v", err)
		}
		cancelOffline()
	}
	if repository != nil {
		closeRepository(repository)
	}
}

func startElection(election *usecase.BullyElectionUseCase, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result, err := election.Start(ctx)
	if err != nil {
		return fmt.Errorf("start Bully election: %w", err)
	}

	if result.Coordinator == nil {
		log.Printf(
			"election state=%s higher_priority=%d acknowledgements=%d",
			result.State,
			result.HigherPriorityNodes,
			result.AcknowledgedCandidates,
		)
		return nil
	}

	log.Printf(
		"election state=%s coordinator=%d address=%s announcement_failures=%v",
		result.State,
		result.Coordinator.ID,
		result.Coordinator.Address,
		result.AnnouncementFailures,
	)
	return nil
}

func initializeRepository(cfg config.Config, coordinator ports.CoordinatorView) *persistence.MongoNodeDocumentRepository {
	mongoContext, cancelMongo := context.WithTimeout(context.Background(), mongoStartupTimeout)
	defer cancelMongo()

	repository, err := persistence.NewMongoNodeDocumentRepository(
		mongoContext,
		cfg.DatabaseURI,
		cfg.DatabaseName,
		cfg.NodeCollection,
		coordinator,
	)
	if err != nil {
		log.Printf(
			"MongoDB unavailable, retrying in %s; node keeps participating in Bully: %v",
			mongoRetryInterval,
			err,
		)
		return nil
	}

	log.Printf("MongoDB connection ready for %s.%s", cfg.DatabaseName, cfg.NodeCollection)
	return repository
}

func closeRepository(repository *persistence.MongoNodeDocumentRepository) {
	closeContext, cancelClose := context.WithTimeout(context.Background(), mongoCloseTimeout)
	defer cancelClose()

	if err := repository.Close(closeContext); err != nil {
		log.Printf("close MongoDB repository: %v", err)
	}
}

func waitForShutdown(
	grpcServer interface{ GracefulStop() },
	serverErrors <-chan error,
	stopRuntime context.CancelFunc,
) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case err := <-serverErrors:
		stopRuntime()
		if err != nil {
			return fmt.Errorf("gRPC server: %w", err)
		}
		return nil
	case received := <-signals:
		log.Printf("received %s, stopping gracefully", received)
		stopRuntime()
		grpcServer.GracefulStop()
		return nil
	}
}
