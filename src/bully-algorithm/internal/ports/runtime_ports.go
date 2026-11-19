package ports

import (
	"context"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/domain/model"
)

// LamportClock es el reloj lógico compartido por los casos de uso del nodo.
// La implementación debe ser segura para uso concurrente, porque el consumidor
// y los publicadores lo usan desde goroutines distintas.
type LamportClock interface {
	Tick() model.LamportTime
	Observe(remote model.LamportTime) model.LamportTime
}

// MessagePublisher envía un mensaje del dominio al broker y espera su confirmación.
type MessagePublisher interface {
	Publish(ctx context.Context, msg model.Message) error
}

// MachineMetrics son las lecturas que el nodo reporta en el tópico metrics.
type MachineMetrics struct {
	CPUTempC      float64
	FreeRAMMB     uint64
	BandwidthMbps float64
}

// MachineMetricsReader obtiene las métricas actuales del host.
type MachineMetricsReader interface {
	ReadMetrics(ctx context.Context) (MachineMetrics, error)
}
