package grpcadapter

import (
	"context"
	"fmt"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

// lamportMetadataKey es la cabecera gRPC que transporta el reloj de Lamport.
// Viaja en cada petición (cliente a servidor) y en cada respuesta (servidor a
// cliente), así cualquier llamada entre nodos sincroniza ambos relojes.
const lamportMetadataKey = "x-lamport-ts"

// LamportClientInterceptor avanza el reloj antes de enviar cada petición unaria
// y lo incorpora al recibir la respuesta.
func LamportClientInterceptor(clock ports.LamportClock) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		sent := clock.Tick()
		ctx = metadata.AppendToOutgoingContext(ctx, lamportMetadataKey, formatLamport(sent))

		var header metadata.MD
		opts = append(opts, grpc.Header(&header))
		err := invoker(ctx, method, req, reply, cc, opts...)

		// Las respuestas de error también pueden traer cabeceras. Si no traen el
		// reloj, no se modifica el reloj local.
		if remote, ok, parseErr := lamportFromMetadata(header); parseErr == nil && ok {
			clock.Observe(remote)
		}
		return err
	}
}

// LamportServerInterceptor incorpora el reloj de la petición entrante antes de
// ejecutar el handler y devuelve el reloj local en la cabecera de respuesta.
func LamportServerInterceptor(clock ports.LamportClock) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			remote, found, err := lamportFromMetadata(md)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid %s header: %v", lamportMetadataKey, err)
			}
			if found {
				clock.Observe(remote)
			}
		}

		reply := clock.Tick()
		if err := grpc.SetHeader(ctx, metadata.Pairs(lamportMetadataKey, formatLamport(reply))); err != nil {
			return nil, status.Errorf(codes.Internal, "set lamport header: %v", err)
		}
		return handler(ctx, req)
	}
}

// lamportFromMetadata lee el reloj de las cabeceras. found=false indica que el
// remitente no envió la cabecera, por ejemplo un nodo con la versión anterior.
func lamportFromMetadata(md metadata.MD) (model.LamportTime, bool, error) {
	values := md.Get(lamportMetadataKey)
	if len(values) == 0 {
		return 0, false, nil
	}
	value, err := strconv.ParseUint(values[0], 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse lamport timestamp %q: %w", values[0], err)
	}
	return model.LamportTime(value), true, nil
}

func formatLamport(ts model.LamportTime) string {
	return strconv.FormatUint(uint64(ts), 10)
}
