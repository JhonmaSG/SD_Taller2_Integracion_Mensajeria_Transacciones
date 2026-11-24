package config

import "testing"

func setRequiredNodeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("NODE_ID", "1")
	t.Setenv("NODE_HOST", "127.0.0.1")
	t.Setenv("DB_URI", "mongodb://localhost:27017")
}

func TestManualMetricsDefaultsToFalse(t *testing.T) {
	setRequiredNodeEnv(t)
	t.Setenv("MANUAL_METRICS", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if cfg.ManualMetrics {
		t.Fatal("MANUAL_METRICS should default to false")
	}
}

func TestManualMetricsParsesTrue(t *testing.T) {
	setRequiredNodeEnv(t)
	t.Setenv("MANUAL_METRICS", "true")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	if !cfg.ManualMetrics {
		t.Fatal("MANUAL_METRICS=true should enable manual metrics")
	}
}

func TestManualMetricsRejectsInvalidValue(t *testing.T) {
	setRequiredNodeEnv(t)
	t.Setenv("MANUAL_METRICS", "si")

	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("MANUAL_METRICS=si should fail")
	}
}
