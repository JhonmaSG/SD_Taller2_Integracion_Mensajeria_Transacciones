package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/JhonmaSG/SD_Taller1_Coordinacion_y_datos_del_sistema/src/bully-algorithm/internal/ports"
)

const (
	thermalZoneGlob = "/sys/class/thermal/thermal_zone*/temp"
	memInfoPath     = "/proc/meminfo"
)

// MachineMetricsSampler lee las métricas del host para el tópico metrics. Hoy
// solo está implementado para Linux. En otros sistemas devuelve error.
type MachineMetricsSampler struct {
	bandwidth func(context.Context) int64
	readFile  func(string) ([]byte, error)
	glob      func(string) ([]string, error)
}

var _ ports.MachineMetricsReader = (*MachineMetricsSampler)(nil)

// NewMachineMetricsSampler crea el lector con las fuentes reales del sistema.
func NewMachineMetricsSampler() *MachineMetricsSampler {
	return &MachineMetricsSampler{
		bandwidth: linkBandwidthMbps,
		readFile:  os.ReadFile,
		glob:      filepath.Glob,
	}
}

// ReadMetrics devuelve temperatura de CPU, RAM disponible y velocidad del enlace.
// La temperatura falla si el sistema no expone sensores térmicos, algo común en
// máquinas virtuales.
func (s *MachineMetricsSampler) ReadMetrics(ctx context.Context) (ports.MachineMetrics, error) {
	if ctx == nil {
		return ports.MachineMetrics{}, errors.New("context must not be nil")
	}
	if runtime.GOOS != "linux" {
		return ports.MachineMetrics{}, fmt.Errorf("machine metrics are not implemented for %s", runtime.GOOS)
	}

	temperature, err := s.cpuTemperatureC()
	if err != nil {
		return ports.MachineMetrics{}, err
	}
	freeRAM, err := s.freeRAMMB()
	if err != nil {
		return ports.MachineMetrics{}, err
	}
	return ports.MachineMetrics{
		CPUTempC:      temperature,
		FreeRAMMB:     freeRAM,
		BandwidthMbps: float64(s.bandwidth(ctx)),
	}, nil
}

func (s *MachineMetricsSampler) cpuTemperatureC() (float64, error) {
	zones, err := s.glob(thermalZoneGlob)
	if err != nil {
		return 0, fmt.Errorf("list thermal zones: %w", err)
	}
	for _, zone := range zones {
		raw, readErr := s.readFile(zone)
		if readErr != nil {
			continue
		}
		if temperature, parseErr := parseThermalMilliC(raw); parseErr == nil {
			return temperature, nil
		}
	}
	return 0, errors.New("no CPU temperature sensor available")
}

func (s *MachineMetricsSampler) freeRAMMB() (uint64, error) {
	raw, err := s.readFile(memInfoPath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", memInfoPath, err)
	}
	return parseMemAvailableMB(raw)
}

// parseThermalMilliC convierte el contenido de thermal_zone*/temp (miligrados) a grados Celsius.
func parseThermalMilliC(raw []byte) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	if err != nil {
		return 0, fmt.Errorf("parse thermal zone: %w", err)
	}
	return value / 1000, nil
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
