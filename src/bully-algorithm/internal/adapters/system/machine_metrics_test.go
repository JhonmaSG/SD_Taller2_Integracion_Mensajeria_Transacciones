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

func TestParseWindowsFreeMemoryMBConvertsKilobytes(t *testing.T) {
	// FreePhysicalMemory llega en kB, con salto de línea al final desde PowerShell.
	got, err := parseWindowsFreeMemoryMB("1048576\r\n")
	if err != nil || got != 1024 {
		t.Fatalf("parseWindowsFreeMemoryMB = %d, %v; want 1024", got, err)
	}
}

func TestParseWindowsFreeMemoryMBRejectsInvalidOutput(t *testing.T) {
	for _, output := range []string{"", "no es un numero", "-5"} {
		if _, err := parseWindowsFreeMemoryMB(output); err == nil {
			t.Fatalf("parseWindowsFreeMemoryMB(%q) should fail", output)
		}
	}
}

func TestMachineMetricsSamplerReportsZeroTempWithoutSensor(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("sampler reads /proc/meminfo on Linux")
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
