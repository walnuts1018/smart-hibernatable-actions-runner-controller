package metrics

import (
	"context"
	"net/http"
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

func TestOTLPHTTPExporter(t *testing.T) {
	var requestedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", srv.URL)
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "100")

	if err := Setup(t.Context(), "metrics-test-otlp"); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	defer func() {
		_ = Shutdown(context.Background())
	}()

	AvailableJobs.WithLabelValues("default", "example").Set(1)

	// Trigger export via shutdown or wait
	if err := provider.ForceFlush(t.Context()); err != nil {
		t.Fatalf("ForceFlush() error = %v", err)
	}

	if requestedPath != "/v1/metrics" {
		t.Errorf("Expected request path to be /v1/metrics, got %s", requestedPath)
	}
}
