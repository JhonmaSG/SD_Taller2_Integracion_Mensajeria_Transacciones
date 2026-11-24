package grpcadapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	agentpb "github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/agent/v1"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

var (
	_ ports.ResourceReservationPort = (*AgentClient)(nil)
	_ ports.AgentNotifier           = (*AgentClient)(nil)
)

// AgentClient es el adaptador de salida que usa el coordinador para ejecutar
// los pasos de la saga en los agentes. Cada llamada tiene un timeout propio.
// Reserve, Release y NotifyAllocation son idempotentes por saga_id, así que es
// seguro repetirlas tras un fallo de red.
type AgentClient struct {
	membership  ports.MembershipPort
	timeout     time.Duration
	dialOptions []grpc.DialOption
}

// NewAgentClient crea el cliente. timeout limita cada llamada RPC. Las opciones
// extra se añaden tras las credenciales sin TLS por defecto.
func NewAgentClient(membership ports.MembershipPort, timeout time.Duration, options ...grpc.DialOption) (*AgentClient, error) {
	if membership == nil {
		return nil, errors.New("membership port must not be nil")
	}
	if timeout <= 0 {
		return nil, errors.New("agent call timeout must be greater than zero")
	}
	dialOptions := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	dialOptions = append(dialOptions, options...)
	return &AgentClient{membership: membership, timeout: timeout, dialOptions: dialOptions}, nil
}

// Reserve pide la reserva en el nodo indicado. Un rechazo del agente no es un
// error: llega en ReservationResult.Accepted=false.
func (c *AgentClient) Reserve(ctx context.Context, sagaID model.SagaID, node model.NodeID, resource model.Resource) (ports.ReservationResult, error) {
	if ctx == nil {
		return ports.ReservationResult{}, errors.New("context must not be nil")
	}
	var result ports.ReservationResult
	err := c.call(ctx, node, func(callCtx context.Context, client agentpb.AgentServiceClient) error {
		response, err := client.Reserve(callCtx, &agentpb.ReserveRequest{
			SagaId:   string(sagaID),
			NodeId:   uint64(node),
			Resource: toAgentResource(resource),
		}, grpc.WaitForReady(true))
		if err != nil {
			return err
		}
		result = ports.ReservationResult{
			Accepted:      response.GetAccepted(),
			ReservationID: response.GetReservationId(),
			Reason:        response.GetReason(),
		}
		return nil
	})
	if err != nil {
		return ports.ReservationResult{}, err
	}
	return result, nil
}

// Release libera la reserva de la saga en el nodo indicado.
func (c *AgentClient) Release(ctx context.Context, sagaID model.SagaID, node model.NodeID, reservationID string) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	return c.call(ctx, node, func(callCtx context.Context, client agentpb.AgentServiceClient) error {
		response, err := client.Release(callCtx, &agentpb.ReleaseRequest{
			SagaId:        string(sagaID),
			NodeId:        uint64(node),
			ReservationId: reservationID,
		}, grpc.WaitForReady(true))
		if err != nil {
			return err
		}
		if !response.GetReleased() {
			return errors.New("agent did not confirm the release")
		}
		return nil
	})
}

// NotifyAllocation informa al agente de su asignación confirmada.
func (c *AgentClient) NotifyAllocation(ctx context.Context, sagaID model.SagaID, node model.NodeID) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	return c.call(ctx, node, func(callCtx context.Context, client agentpb.AgentServiceClient) error {
		response, err := client.NotifyAllocation(callCtx, &agentpb.NotifyAllocationRequest{
			SagaId: string(sagaID),
			NodeId: uint64(node),
		}, grpc.WaitForReady(true))
		if err != nil {
			return err
		}
		if !response.GetAcknowledged() {
			return errors.New("agent did not acknowledge the allocation")
		}
		return nil
	})
}

// call abre una conexión al nodo, ejecuta fn con un contexto limitado por el
// timeout del cliente y cierra la conexión.
func (c *AgentClient) call(ctx context.Context, node model.NodeID, fn func(context.Context, agentpb.AgentServiceClient) error) error {
	address, err := c.addressOf(node)
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(address, c.dialOptions...)
	if err != nil {
		return fmt.Errorf("dial agent node %d: %w", node, err)
	}
	defer conn.Close()

	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := fn(callCtx, agentpb.NewAgentServiceClient(conn)); err != nil {
		return fmt.Errorf("agent node %d: %w", node, err)
	}
	return nil
}

// addressOf busca la dirección de un nodo en la membresía local.
func (c *AgentClient) addressOf(node model.NodeID) (string, error) {
	local := c.membership.LocalNode()
	if local.ID == node {
		return local.Address, nil
	}
	for _, peer := range c.membership.Peers() {
		if peer.ID == node {
			return peer.Address, nil
		}
	}
	return "", fmt.Errorf("node %d is not part of the membership", node)
}

func toAgentResource(resource model.Resource) *agentpb.Resource {
	return &agentpb.Resource{
		Cpu:           resource.CPU,
		RamMb:         resource.RAMMB,
		BandwidthMbps: resource.BandwidthMbps,
	}
}
