package system

import (
	"context"
	"errors"
	"runtime"
	"testing"
)

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

func TestMachineMetricsSamplerReportsZeroTempWithoutSensor(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sampler only reads Linux sources")
	}
	sampler := &MachineMetricsSampler{
		bandwidth: func(context.Context) int64 { return 100 },
		readFile: func(path string) ([]byte, error) {
			if path != memInfoPath {
				t.Fatalf("unexpected read of %s", path)
			}
			return []byte("MemAvailable: 2048 kB\n"), nil
		},
	}
	got, err := sampler.ReadMetrics(context.Background())
	if err != nil {
		t.Fatalf("ReadMetrics: %v", err)
	}
	if got.CPUTempC != 0 || got.FreeRAMMB != 2 || got.BandwidthMbps != 100 {
		t.Fatalf("metrics = %+v", got)
	}
}

func TestMachineMetricsSamplerRejectsNilContext(t *testing.T) {
	sampler := NewMachineMetricsSampler()
	if _, err := sampler.ReadMetrics(nil); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("nil context should be rejected")
	}
}
