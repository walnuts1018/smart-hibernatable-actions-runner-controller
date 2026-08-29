package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	ColdStartSeconds.WithLabelValues("default", "machine").Observe(t.Context(), 15.5)

	recorder := httptest.NewRecorder()
	Handler().ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))

	body := recorder.Body.String()
	if !strings.Contains(body, `gha_available_jobs{name="example",namespace="default"} 3`) {
		t.Errorf("Prometheus output does not contain the OpenTelemetry gauge: %s", body)
	}
	if !strings.Contains(body, `gha_baremetal_power_transitions_total{action="PowerOn",name="machine",namespace="default"} 1`) {
		t.Errorf("Prometheus output does not preserve the counter name: %s", body)
	}
	if !strings.Contains(body, `gha_baremetal_cold_start_seconds_bucket`) {
		t.Errorf("Prometheus output does not contain histogram: %s", body)
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

func TestNoneExporterAndShutdown(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	if err := Setup(t.Context(), "metrics-none"); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if Handler() != nil {
		t.Errorf("expected nil handler for none exporter")
	}

	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestServePrometheus(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "prometheus")
	t.Setenv("OTEL_EXPORTER_PROMETHEUS_HOST", "127.0.0.1")
	t.Setenv("OTEL_EXPORTER_PROMETHEUS_PORT", "19464")

	if err := Setup(t.Context(), "metrics-serve"); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	defer func() {
		_ = Shutdown(context.Background())
	}()

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx)
	}()

	// Allow server to spin up
	time.Sleep(50 * time.Millisecond)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:19464/metrics", nil)
	if err != nil {
		t.Fatalf("failed to create prometheus request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to query prometheus server: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from prometheus server, got %d", resp.StatusCode)
	}

	// Cancel context to stop Serve
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("unexpected error from Serve shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not exit within timeout after cancel")
	}
}

func TestValidationErrors(t *testing.T) {
	// Multiple exporters
	t.Setenv("OTEL_METRICS_EXPORTER", "prometheus,otlp")
	if err := Setup(t.Context(), "test"); err == nil {
		t.Errorf("expected error for multiple exporters")
	}

	// Unsupported exporter
	t.Setenv("OTEL_METRICS_EXPORTER", "unsupported-exporter")
	if err := Setup(t.Context(), "test"); err == nil {
		t.Errorf("expected error for unsupported exporter")
	}

	// Invalid interval
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "invalid-num")
	if err := Setup(t.Context(), "test"); err == nil {
		t.Errorf("expected error for invalid interval")
	}
}
