package grpcadapter

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

const lamportTestMethod = "/lamporttest.Clock/Ping"

// lamportTestClock implementa ports.LamportClock con un valor protegido por mutex.
type lamportTestClock struct {
	mu    sync.Mutex
	value model.LamportTime
}

func (c *lamportTestClock) Tick() model.LamportTime {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value++
	return c.value
}

func (c *lamportTestClock) Observe(remote model.LamportTime) model.LamportTime {
	c.mu.Lock()
	defer c.mu.Unlock()
	if remote > c.value {
		c.value = remote
	}
	c.value++
	return c.value
}

func (c *lamportTestClock) get() model.LamportTime {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// pingServiceDesc describe un método unario sin protobuf propio, usando Empty
// como mensaje. Así el test no depende de los servicios del proyecto.
var pingServiceDesc = grpc.ServiceDesc{
	ServiceName: "lamporttest.Clock",
	HandlerType: (*any)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "Ping",
		Handler: func(_ any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			in := new(emptypb.Empty)
			if err := dec(in); err != nil {
				return nil, err
			}
			handler := func(context.Context, any) (any, error) { return new(emptypb.Empty), nil }
			if interceptor == nil {
				return handler(ctx, in)
			}
			return interceptor(ctx, in, &grpc.UnaryServerInfo{FullMethod: lamportTestMethod}, handler)
		},
	}},
	Metadata: "lamporttest",
}

// startLamportServer levanta un servidor con el interceptor de Lamport y devuelve su dirección.
func startLamportServer(t *testing.T, clock *lamportTestClock) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(LamportServerInterceptor(clock)))
	server.RegisterService(&pingServiceDesc, struct{}{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

// callPing invoca el método con un cliente que usa el interceptor de Lamport.
func callPing(t *testing.T, address string, clock *lamportTestClock) {
	t.Helper()
	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(LamportClientInterceptor(clock)),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Invoke(ctx, lamportTestMethod, new(emptypb.Empty), new(emptypb.Empty)); err != nil {
		t.Fatalf("invoke: %v", err)
	}
}

func TestLamportClockAdvancesPastRemoteWhenClientIsAhead(t *testing.T) {
	server := &lamportTestClock{}
	client := &lamportTestClock{value: 100}
	address := startLamportServer(t, server)

	callPing(t, address, client)

	if got := server.get(); got < 101 {
		t.Fatalf("server clock = %d, want at least 101 (client sent 101)", got)
	}
	if got := client.get(); got < server.get() {
		t.Fatalf("client clock = %d, want at least the server clock %d", got, server.get())
	}
}

// Caso del seguidor atrasado: el coordinador tiene un reloj alto y el seguidor
// queda por detrás. Tras una llamada, el seguidor debe quedar por encima de la
// marca de tiempo del coordinador.
func TestLamportClockCatchesUpWithRemoteWhenServerIsAhead(t *testing.T) {
	server := &lamportTestClock{value: 500}
	client := &lamportTestClock{value: 1}
	address := startLamportServer(t, server)

	callPing(t, address, client)

	if got := client.get(); got <= 500 {
		t.Fatalf("client clock = %d, want greater than the server clock 500", got)
	}
}
