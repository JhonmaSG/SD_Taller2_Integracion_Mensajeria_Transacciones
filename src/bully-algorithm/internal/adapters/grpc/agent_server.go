package grpcadapter

import (
	"context"
	"errors"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentpb "github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/agent/v1"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/usecase"
)

// AgentServer es el adaptador de entrada gRPC del agente. Convierte los mensajes
// a modelos de dominio y delega en AgentReservationUseCase.
type AgentServer struct {
	agentpb.UnimplementedAgentServiceServer
	useCase *usecase.AgentReservationUseCase
}

// NewAgentServer crea el servidor del agente sobre el libro de reservas local.
func NewAgentServer(useCase *usecase.AgentReservationUseCase) (*AgentServer, error) {
	if useCase == nil {
		return nil, errors.New("agent reservation use case must not be nil")
	}
	return &AgentServer{useCase: useCase}, nil
}

// Register añade el servicio AgentService a un servidor gRPC existente.
func (s *AgentServer) Register(registrar grpc.ServiceRegistrar) {
	agentpb.RegisterAgentServiceServer(registrar, s)
}

// Reserve procesa la reserva. Una reserva repetida devuelve la reserva ya existente.
func (s *AgentServer) Reserve(_ context.Context, request *agentpb.ReserveRequest) (*agentpb.ReserveResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "reserve request must not be nil")
	}
	resource, err := fromAgentResource(request.GetResource())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	outcome, err := s.useCase.Reserve(model.SagaID(request.GetSagaId()), model.NodeID(request.GetNodeId()), resource)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return &agentpb.ReserveResponse{
		Accepted:      outcome.Accepted,
		ReservationId: outcome.ReservationID,
		Reason:        outcome.Reason,
	}, nil
}

// Release libera la reserva. Si la saga no tiene reserva, responde released=true.
func (s *AgentServer) Release(_ context.Context, request *agentpb.ReleaseRequest) (*agentpb.ReleaseResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "release request must not be nil")
	}
	if err := s.useCase.Release(model.SagaID(request.GetSagaId()), request.GetReservationId()); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	return &agentpb.ReleaseResponse{Released: true}, nil
}

// NotifyAllocation marca la reserva como confirmada. Siempre responde
// acknowledged=true para que el coordinador no reintente indefinidamente. Si el
// agente no tenía la reserva, lo registra en el log.
func (s *AgentServer) NotifyAllocation(_ context.Context, request *agentpb.NotifyAllocationRequest) (*agentpb.NotifyAllocationResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "notify request must not be nil")
	}
	found, err := s.useCase.Confirm(model.SagaID(request.GetSagaId()))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	if !found {
		log.Printf("allocation notification for saga=%s without local reservation", request.GetSagaId())
	}
	return &agentpb.NotifyAllocationResponse{Acknowledged: true}, nil
}

func fromAgentResource(resource *agentpb.Resource) (model.Resource, error) {
	if resource == nil {
		return model.Resource{}, errors.New("resource must not be nil")
	}
	return model.Resource{
		CPU:           resource.GetCpu(),
		RAMMB:         resource.GetRamMb(),
		BandwidthMbps: resource.GetBandwidthMbps(),
	}, nil
}
