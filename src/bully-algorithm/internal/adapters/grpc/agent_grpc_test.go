package grpcadapter

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/usecase"
)

type agentTestMembership struct {
	local model.Node
	peers []model.Node
}

func (m agentTestMembership) LocalNode() model.Node { return m.local }
func (m agentTestMembership) Peers() []model.Node   { return m.peers }

const agentTestAddress = "passthrough:///agent-2"

// newAgentTestClient levanta un AgentServer sobre bufconn y devuelve un cliente
// conectado a él.
func newAgentTestClient(t *testing.T, timeout time.Duration) (*AgentClient, *bufconn.Listener) {
	t.Helper()
	ledger, err := usecase.NewAgentReservationUseCase(2, model.Resource{CPU: 4, RAMMB: 4096, BandwidthMbps: 1000})
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	server, err := NewAgentServer(ledger)
	if err != nil {
		t.Fatalf("NewAgentServer: %v", err)
	}
	lis := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	server.Register(grpcServer)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	client, err := NewAgentClient(
		agentTestMembership{local: model.Node{ID: 2, Address: agentTestAddress}},
		timeout,
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("NewAgentClient: %v", err)
	}
	return client, lis
}

func agentTestResource(ram uint64) model.Resource {
	return model.Resource{CPU: 1, RAMMB: ram, BandwidthMbps: 10}
}

func TestAgentGRPCReserveIsIdempotent(t *testing.T) {
	client, _ := newAgentTestClient(t, 2*time.Second)
	ctx := context.Background()

	first, err := client.Reserve(ctx, "saga-1", 2, agentTestResource(1000))
	if err != nil || !first.Accepted || first.ReservationID == "" {
		t.Fatalf("first reserve = %+v, %v", first, err)
	}
	again, err := client.Reserve(ctx, "saga-1", 2, agentTestResource(1000))
	if err != nil || !again.Accepted || again.ReservationID != first.ReservationID {
		t.Fatalf("repeated reserve = %+v, %v", again, err)
	}
}

func TestAgentGRPCReserveReportsRejectionReason(t *testing.T) {
	client, _ := newAgentTestClient(t, 2*time.Second)
	ctx := context.Background()

	if _, err := client.Reserve(ctx, "saga-1", 2, agentTestResource(3000)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	out, err := client.Reserve(ctx, "saga-2", 2, agentTestResource(2000))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if out.Accepted || out.Reason != usecase.ReasonInsufficientCapacity {
		t.Fatalf("outcome = %+v, want rejected INSUFFICIENT_CAPACITY", out)
	}
}

func TestAgentGRPCReleaseFreesCapacity(t *testing.T) {
	client, _ := newAgentTestClient(t, 2*time.Second)
	ctx := context.Background()

	if _, err := client.Reserve(ctx, "saga-1", 2, agentTestResource(3000)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := client.Release(ctx, "saga-1", 2, "res-saga-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := client.Release(ctx, "saga-1", 2, "res-saga-1"); err != nil {
		t.Fatalf("second Release should be idempotent: %v", err)
	}
	out, err := client.Reserve(ctx, "saga-2", 2, agentTestResource(3000))
	if err != nil || !out.Accepted {
		t.Fatalf("reserve after release = %+v, %v", out, err)
	}
}

func TestAgentGRPCNotifyAllocationAcknowledges(t *testing.T) {
	client, _ := newAgentTestClient(t, 2*time.Second)
	ctx := context.Background()

	if _, err := client.Reserve(ctx, "saga-1", 2, agentTestResource(100)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := client.NotifyAllocation(ctx, "saga-1", 2); err != nil {
		t.Fatalf("NotifyAllocation: %v", err)
	}
	// Tras confirmar, la reserva ya no puede liberarse.
	if err := client.Release(ctx, "saga-1", 2, ""); err == nil {
		t.Fatal("release after confirmation should fail")
	}
	// Una notificación sin reserva local también se reconoce.
	if err := client.NotifyAllocation(ctx, "saga-lost", 2); err != nil {
		t.Fatalf("NotifyAllocation without reservation: %v", err)
	}
}

func TestAgentClientRejectsNodeOutsideMembership(t *testing.T) {
	client, _ := newAgentTestClient(t, 2*time.Second)
	_, err := client.Reserve(context.Background(), "saga-1", 9, agentTestResource(100))
	if err == nil || !strings.Contains(err.Error(), "not part of the membership") {
		t.Fatalf("err = %v, want membership error", err)
	}
}

func TestAgentClientTimesOutWhenAgentDoesNotRespond(t *testing.T) {
	// El listener existe pero nadie sirve gRPC en él: la llamada no recibe respuesta.
	lis := bufconn.Listen(1 << 20)
	t.Cleanup(func() { _ = lis.Close() })
	client, err := NewAgentClient(
		agentTestMembership{local: model.Node{ID: 2, Address: agentTestAddress}},
		150*time.Millisecond,
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("NewAgentClient: %v", err)
	}

	start := time.Now()
	_, err = client.Reserve(context.Background(), "saga-1", 2, agentTestResource(100))
	if err == nil {
		t.Fatal("reserve against silent agent should fail")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("call took %s, timeout not applied", elapsed)
	}
}

func TestNewAgentClientValidatesArguments(t *testing.T) {
	m := agentTestMembership{local: model.Node{ID: 1, Address: "x"}}
	if _, err := NewAgentClient(nil, time.Second); err == nil {
		t.Fatal("nil membership should fail")
	}
	if _, err := NewAgentClient(m, 0); err == nil {
		t.Fatal("zero timeout should fail")
	}
}
