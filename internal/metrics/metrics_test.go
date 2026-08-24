package metrics

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrometheusExporterCollectsOpenTelemetryMetrics(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "prometheus")
	if err := Setup(t.Context(), "metrics-test"); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	t.Cleanup(func() {
		if err := Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})

	AvailableJobs.WithLabelValues("default", "example").Set(3)
	PowerTransitionsTotal.WithLabelValues("default", "machine", "PowerOn").Inc(t.Context())
	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))

	body := recorder.Body.String()
	if !strings.Contains(body, `gha_available_jobs{name="example",namespace="default"} 3`) {
		t.Errorf("Prometheus output does not contain the OpenTelemetry gauge: %s", body)
	}
	if !strings.Contains(body, `gha_baremetal_power_transitions_total{action="PowerOn",name="machine",namespace="default"} 1`) {
		t.Errorf("Prometheus output does not preserve the counter name: %s", body)
	}
}
