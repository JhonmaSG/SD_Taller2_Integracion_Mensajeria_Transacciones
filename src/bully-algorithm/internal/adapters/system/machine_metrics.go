package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

const memInfoPath = "/proc/meminfo"

// MachineMetricsSampler lee las métricas del host para el tópico metrics. Hoy
// solo está implementado para Linux. En otros sistemas devuelve error.
type MachineMetricsSampler struct {
	bandwidth func(context.Context) int64
	readFile  func(string) ([]byte, error)
}

var _ ports.MachineMetricsReader = (*MachineMetricsSampler)(nil)

// NewMachineMetricsSampler crea el lector con las fuentes reales del sistema.
func NewMachineMetricsSampler() *MachineMetricsSampler {
	return &MachineMetricsSampler{
		bandwidth: linkBandwidthMbps,
		readFile:  os.ReadFile,
	}
}

// ReadMetrics devuelve RAM disponible y velocidad del enlace. La temperatura de
// CPU no se lee: CPUTempC siempre es 0, que significa "sin lectura".
func (s *MachineMetricsSampler) ReadMetrics(ctx context.Context) (ports.MachineMetrics, error) {
	if ctx == nil {
		return ports.MachineMetrics{}, errors.New("context must not be nil")
	}
	if runtime.GOOS != "linux" {
		return ports.MachineMetrics{}, fmt.Errorf("machine metrics are not implemented for %s", runtime.GOOS)
	}

	freeRAM, err := s.freeRAMMB()
	if err != nil {
		return ports.MachineMetrics{}, err
	}
	return ports.MachineMetrics{
		CPUTempC:      0,
		FreeRAMMB:     freeRAM,
		BandwidthMbps: float64(s.bandwidth(ctx)),
	}, nil
}

func (s *MachineMetricsSampler) freeRAMMB() (uint64, error) {
	raw, err := s.readFile(memInfoPath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", memInfoPath, err)
	}
	return parseMemAvailableMB(raw)
}

// parseMemAvailableMB extrae MemAvailable (en kB) de /proc/meminfo y lo convierte a MB.
func parseMemAvailableMB(raw []byte) (uint64, error) {
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "MemAvailable:" {
			continue
		}
		kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse MemAvailable: %w", err)
		}
		return kilobytes / 1024, nil
	}
	return 0, errors.New("MemAvailable not found in meminfo")
}
