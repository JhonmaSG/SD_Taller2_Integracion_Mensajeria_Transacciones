package system

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

func TestParseThermalMilliCConvertsToCelsius(t *testing.T) {
	got, err := parseThermalMilliC([]byte("54321\n"))
	if err != nil || got != 54.321 {
		t.Fatalf("parseThermalMilliC = %v, %v; want 54.321", got, err)
	}
	if _, err := parseThermalMilliC([]byte("abc")); err == nil {
		t.Fatal("non-numeric thermal zone should fail")
	}
}

func TestParseMemAvailableMBReadsMemAvailable(t *testing.T) {
	meminfo := []byte("MemTotal:       2048000 kB\nMemFree:         100000 kB\nMemAvailable:   1048576 kB\nBuffers:          1000 kB\n")
	got, err := parseMemAvailableMB(meminfo)
	if err != nil || got != 1024 {
		t.Fatalf("parseMemAvailableMB = %d, %v; want 1024", got, err)
	}
	if _, err := parseMemAvailableMB([]byte("MemTotal: 1 kB\n")); err == nil {
		t.Fatal("missing MemAvailable should fail")
	}
}

func TestMachineMetricsSamplerFailsWithoutThermalSensor(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sampler only reads Linux sources")
	}
	sampler := &MachineMetricsSampler{
		bandwidth: func(context.Context) int64 { return 100 },
		readFile: func(string) ([]byte, error) {
			return []byte("MemAvailable: 1024 kB\n"), nil
		},
		glob: func(string) ([]string, error) { return nil, nil },
	}
	if _, err := sampler.ReadMetrics(context.Background()); err == nil {
		t.Fatal("absent thermal sensor should be reported as an error")
	}
}

func TestMachineMetricsSamplerReadsAllSourcesOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sampler only reads Linux sources")
	}
	sampler := &MachineMetricsSampler{
		bandwidth: func(context.Context) int64 { return 100 },
		readFile: func(path string) ([]byte, error) {
			if path == memInfoPath {
				return []byte("MemAvailable: 2048 kB\n"), nil
			}
			return []byte("45000\n"), nil
		},
		glob: func(string) ([]string, error) { return []string{"/zone0/temp"}, nil },
	}
	got, err := sampler.ReadMetrics(context.Background())
	if err != nil {
		t.Fatalf("ReadMetrics: %v", err)
	}
	if got.CPUTempC != 45 || got.FreeRAMMB != 2 || got.BandwidthMbps != 100 {
		t.Fatalf("metrics = %+v", got)
	}
}

func TestMachineMetricsSamplerRejectsNilContext(t *testing.T) {
	sampler := NewMachineMetricsSampler()
	if _, err := sampler.ReadMetrics(nil); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("nil context should be rejected")
	}
}
