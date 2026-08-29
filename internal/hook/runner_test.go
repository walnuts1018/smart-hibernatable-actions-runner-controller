package hook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRunJobStartedAndCompleted(t *testing.T) {
	// Setup temporary directory for cgroups and worker log
	tmpDir := t.TempDir()

	// Mock cgroup controller files
	cgroupDir := filepath.Join(tmpDir, "kubepods.slice", "pod123")
	if err := os.MkdirAll(cgroupDir, 0755); err != nil {
		t.Fatalf("failed to create cgroup dir: %v", err)
	}

	cpuStatFile := filepath.Join(cgroupDir, "cpu.stat")
	cpuContent := "usage_usec 1000000\nuser_usec 800000\nsystem_usec 200000\n"
	if err := os.WriteFile(cpuStatFile, []byte(cpuContent), 0644); err != nil {
		t.Fatalf("failed to write cpu.stat: %v", err)
	}

	memPeakFile := filepath.Join(cgroupDir, "memory.peak")
	if err := os.WriteFile(memPeakFile, []byte("524288000\n"), 0644); err != nil {
		t.Fatalf("failed to write memory.peak: %v", err)
	}

	// Mock worker log directory
	diagDir := filepath.Join(tmpDir, "_diag")
	if err := os.MkdirAll(diagDir, 0755); err != nil {
		t.Fatalf("failed to create diag dir: %v", err)
	}
	workerLog := filepath.Join(diagDir, "Worker_20260824-120000.log")
	logContent := `[2026-08-24 12:00:00Z INFO Worker] Job message:
{
  "jobId": "12345",
  "jobDisplayName": "Test and Build Job",
  "requestId": 1
}`
	if err := os.WriteFile(workerLog, []byte(logContent), 0644); err != nil {
		t.Fatalf("failed to write worker log: %v", err)
	}

	// Environment variables
	t.Setenv("RUNNER_METRICS_CGROUP_ROOT", tmpDir)
	t.Setenv("POD_UID", "123")
	t.Setenv("RUNNER_WORKSPACE", filepath.Join(tmpDir, "_work", "repo"))
	t.Setenv("GITHUB_JOB", "fallback-job")
	t.Setenv("GITHUB_WORKFLOW", "CI")
	t.Setenv("GITHUB_REPOSITORY", "walnuts1018/sharc")
	t.Setenv("GITHUB_JOB_STATUS", "success")
	t.Setenv("POD_NAMESPACE", "gha-runners")
	t.Setenv("POD_NAME", "runner-pod-1")
	t.Setenv("NODE_NAME", "node-1")
	t.Setenv("RUNNER_METRICS_EXTRA_ATTRIBUTES", "cluster=test-cluster")

	logger := slog.New(slog.DiscardHandler)

	// 1. Test RunJobStarted
	if err := RunJobStarted(context.Background(), logger); err != nil {
		t.Fatalf("RunJobStarted failed: %v", err)
	}

	// Verify state file was written
	raw, err := os.ReadFile(defaultStateFilePath)
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(defaultStateFilePath)
	})

	var startData JobStartData
	if err := json.Unmarshal(raw, &startData); err != nil {
		t.Fatalf("failed to unmarshal state file: %v", err)
	}
	if startData.StartCPUUsageUsec != 1000000 {
		t.Errorf("expected StartCPUUsageUsec 1000000, got %d", startData.StartCPUUsageUsec)
	}
	if startData.StartMemoryPeak != 524288000 {
		t.Errorf("expected StartMemoryPeak 524288000, got %d", startData.StartMemoryPeak)
	}

	// 2. Update cpu.stat to simulate work done
	cpuContentUpdated := "usage_usec 3000000\nuser_usec 2400000\nsystem_usec 600000\n"
	if err := os.WriteFile(cpuStatFile, []byte(cpuContentUpdated), 0644); err != nil {
		t.Fatalf("failed to write updated cpu.stat: %v", err)
	}

	// 3. Setup mock OTLP endpoint
	var receivedPayload OtlpPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	t.Setenv("RUNNER_METRICS_ENDPOINT", ts.URL)

	// 4. Test RunJobCompleted
	if err := RunJobCompleted(context.Background(), logger); err != nil {
		t.Fatalf("RunJobCompleted failed: %v", err)
	}

	if len(receivedPayload.ResourceMetrics) == 0 {
		t.Fatalf("expected OTLP payload received")
	}

	// 5. Test RunJobCompleted without RUNNER_METRICS_ENDPOINT (skips export)
	t.Setenv("RUNNER_METRICS_ENDPOINT", "")
	if err := RunJobCompleted(context.Background(), logger); err != nil {
		t.Fatalf("RunJobCompleted without endpoint failed: %v", err)
	}
}
