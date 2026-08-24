package hook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBuildOtlpPayload(t *testing.T) {
	now := time.Unix(1700000000, 123456789)
	mv := MetricValues{
		PeakMemoryBytes: 2147483648,
		AvgCPUCores:     2.5,
		DurationSeconds: 120,
		JobName:         "Build and Test",
		WorkflowName:    "CI Pipeline",
		Repository:      "walnuts1018/my-repo",
		JobResult:       "success",
		Namespace:       "arc-runners",
		PodName:         "runner-pod-xyz",
		NodeName:        "node-01",
		ExtraAttributes: map[string]string{
			"env": "production",
		},
	}

	payload := BuildOtlpPayload(mv, now)

	if len(payload.ResourceMetrics) != 1 {
		t.Fatalf("expected 1 resource metric, got %d", len(payload.ResourceMetrics))
	}

	rm := payload.ResourceMetrics[0]
	var serviceName string
	for _, attr := range rm.Resource.Attributes {
		if attr.Key == "service.name" {
			serviceName = attr.Value.StringValue
		}
	}
	if serviceName != "actions-runner" {
		t.Errorf("expected service.name 'actions-runner', got %q", serviceName)
	}

	if len(rm.ScopeMetrics) != 1 || len(rm.ScopeMetrics[0].Metrics) != 3 {
		t.Fatalf("expected 3 metrics in scope, got %d", len(rm.ScopeMetrics[0].Metrics))
	}

	metricsMap := make(map[string]OtlpMetric)
	for _, m := range rm.ScopeMetrics[0].Metrics {
		metricsMap[m.Name] = m
	}

	// 1. Peak memory
	peakM, ok := metricsMap["gha_job_resource_peak_memory_bytes"]
	if !ok || len(peakM.Gauge.DataPoints) != 1 {
		t.Fatalf("missing peak memory metric")
	}
	if peakM.Gauge.DataPoints[0].AsInt == nil || *peakM.Gauge.DataPoints[0].AsInt != 2147483648 {
		t.Errorf("expected 2147483648 bytes, got %v", peakM.Gauge.DataPoints[0].AsInt)
	}

	// 2. Avg CPU
	avgCPUM, ok := metricsMap["gha_job_resource_avg_cpu_cores"]
	if !ok || len(avgCPUM.Gauge.DataPoints) != 1 {
		t.Fatalf("missing avg cpu metric")
	}
	if avgCPUM.Gauge.DataPoints[0].AsDouble == nil || *avgCPUM.Gauge.DataPoints[0].AsDouble != 2.5 {
		t.Errorf("expected 2.5 cores, got %v", avgCPUM.Gauge.DataPoints[0].AsDouble)
	}

	// 3. Duration
	durM, ok := metricsMap["gha_job_resource_duration_seconds"]
	if !ok || len(durM.Gauge.DataPoints) != 1 {
		t.Fatalf("missing duration metric")
	}
	if durM.Gauge.DataPoints[0].AsInt == nil || *durM.Gauge.DataPoints[0].AsInt != 120 {
		t.Errorf("expected 120s, got %v", durM.Gauge.DataPoints[0].AsInt)
	}
}

func TestSendOtlpMetrics(t *testing.T) {
	var receivedPayload OtlpPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
		}
		if err := json.Unmarshal(body, &receivedPayload); err != nil {
			t.Errorf("failed to unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	mv := MetricValues{
		PeakMemoryBytes: 1024,
		AvgCPUCores:     1.0,
		DurationSeconds: 10,
		JobName:         "test",
	}
	payload := BuildOtlpPayload(mv, time.Now())

	err := SendOtlpMetrics(context.Background(), ts.URL, payload)
	if err != nil {
		t.Fatalf("unexpected error sending metrics: %v", err)
	}

	if len(receivedPayload.ResourceMetrics) != 1 {
		t.Errorf("expected 1 resource metric received, got %d", len(receivedPayload.ResourceMetrics))
	}
}
