package hook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
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
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			// Simulate transient 500 failure
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
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

	if attempts != 2 {
		t.Errorf("expected 2 attempts with retry, got %d", attempts)
	}

	if len(receivedPayload.ResourceMetrics) != 1 {
		t.Errorf("expected 1 resource metric received, got %d", len(receivedPayload.ResourceMetrics))
	}
}

func TestSendOtlpMetrics_Failure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	payload := OtlpPayload{}
	err := SendOtlpMetrics(context.Background(), ts.URL, payload)
	if err == nil {
		t.Fatalf("expected error on 400 Bad Request, got nil")
	}
}

func TestParseExtraAttributes(t *testing.T) {
	// Empty string
	if attrs := ParseExtraAttributes(""); attrs != nil {
		t.Errorf("expected nil for empty string, got %v", attrs)
	}

	// JSON format
	jsonStr := `{"env":"staging","team":"infra"}`
	expected := map[string]string{
		"env":  "staging",
		"team": "infra",
	}
	if attrs := ParseExtraAttributes(jsonStr); !reflect.DeepEqual(attrs, expected) {
		t.Errorf("expected %v, got %v", expected, attrs)
	}

	// Comma separated format
	csvStr := `env=staging, team=infra`
	if attrs := ParseExtraAttributes(csvStr); !reflect.DeepEqual(attrs, expected) {
		t.Errorf("expected %v, got %v", expected, attrs)
	}
}

func TestPopulateMetricValues(t *testing.T) {
	t.Setenv("GITHUB_JOB", "build-job")
	t.Setenv("GITHUB_WORKFLOW", "build-wf")
	t.Setenv("GITHUB_REPOSITORY", "org/repo")
	t.Setenv("GITHUB_JOB_STATUS", "failure")
	t.Setenv("POD_NAMESPACE", "test-ns")
	t.Setenv("POD_NAME", "test-pod")
	t.Setenv("NODE_NAME", "test-node")
	t.Setenv("RUNNER_METRICS_EXTRA_ATTRIBUTES", `{"tier":"backend"}`)

	mv := &MetricValues{}
	PopulateMetricValues(mv)

	if mv.JobName != "build-job" {
		t.Errorf("expected JobName build-job, got %s", mv.JobName)
	}
	if mv.WorkflowName != "build-wf" {
		t.Errorf("expected WorkflowName build-wf, got %s", mv.WorkflowName)
	}
	if mv.Repository != "org/repo" {
		t.Errorf("expected Repository org/repo, got %s", mv.Repository)
	}
	if mv.JobResult != "failure" {
		t.Errorf("expected JobResult failure, got %s", mv.JobResult)
	}
	if mv.Namespace != "test-ns" {
		t.Errorf("expected Namespace test-ns, got %s", mv.Namespace)
	}
	if mv.PodName != "test-pod" {
		t.Errorf("expected PodName test-pod, got %s", mv.PodName)
	}
	if mv.NodeName != "test-node" {
		t.Errorf("expected NodeName test-node, got %s", mv.NodeName)
	}
	if mv.ExtraAttributes["tier"] != "backend" {
		t.Errorf("expected ExtraAttributes tier=backend, got %v", mv.ExtraAttributes)
	}

	// Clear environment variables and test fallback to DefaultUnknownValue
	_ = os.Unsetenv("GITHUB_JOB")
	_ = os.Unsetenv("GITHUB_WORKFLOW")
	_ = os.Unsetenv("GITHUB_REPOSITORY")

	emptyMv := &MetricValues{}
	PopulateMetricValues(emptyMv)
	if emptyMv.JobName != DefaultUnknownValue {
		t.Errorf("expected %s, got %s", DefaultUnknownValue, emptyMv.JobName)
	}
	if emptyMv.WorkflowName != DefaultUnknownValue {
		t.Errorf("expected %s, got %s", DefaultUnknownValue, emptyMv.WorkflowName)
	}
	if emptyMv.Repository != DefaultUnknownValue {
		t.Errorf("expected %s, got %s", DefaultUnknownValue, emptyMv.Repository)
	}
}
