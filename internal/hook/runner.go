package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

const defaultStateFilePath = "/tmp/job-metrics.json"

// RunJobStarted executes the ACTIONS_RUNNER_HOOK_JOB_STARTED logic.
func RunJobStarted(ctx context.Context, logger *slog.Logger) error {
	now := time.Now()

	cgroupRoot := os.Getenv("RUNNER_METRICS_CGROUP_ROOT")
	podUID := os.Getenv("POD_UID")

	cgroupPath := FindPodCgroup(cgroupRoot, podUID, "/sys/fs/cgroup")
	logger.Info("found pod cgroup for job-started", "cgroupPath", cgroupPath, "podUID", podUID)

	startCPU, err := ReadCPUUsageUsec(cgroupPath)
	if err != nil {
		logger.Warn("failed to read start cpu usage", "error", err)
	}

	startMem, err := ReadMemoryPeakBytes(cgroupPath)
	if err != nil {
		logger.Warn("failed to read start memory peak", "error", err)
	}

	data := JobStartData{
		StartTime:         now,
		StartCPUUsageUsec: startCPU,
		StartMemoryPeak:   startMem,
		CgroupPath:        cgroupPath,
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal job start data: %w", err)
	}

	if err := os.WriteFile(defaultStateFilePath, raw, 0600); err != nil {
		return fmt.Errorf("failed to write state file %s: %w", defaultStateFilePath, err)
	}

	logger.Info("job-started recorded successfully", "startTime", now, "cpuUsec", startCPU, "memPeak", startMem)
	return nil
}

// RunJobCompleted executes the ACTIONS_RUNNER_HOOK_JOB_COMPLETED logic.
func RunJobCompleted(ctx context.Context, logger *slog.Logger) error {
	now := time.Now()

	var startData JobStartData
	if raw, err := os.ReadFile(defaultStateFilePath); err == nil {
		_ = json.Unmarshal(raw, &startData)
	}

	if startData.StartTime.IsZero() {
		startData.StartTime = now.Add(-1 * time.Second)
	}

	durationSec := int64(now.Sub(startData.StartTime).Seconds())
	if durationSec <= 0 {
		durationSec = 1
	}

	cgroupPath := startData.CgroupPath
	if cgroupPath == "" {
		cgroupRoot := os.Getenv("RUNNER_METRICS_CGROUP_ROOT")
		podUID := os.Getenv("POD_UID")
		cgroupPath = FindPodCgroup(cgroupRoot, podUID, "/sys/fs/cgroup")
	}

	endCPU, err := ReadCPUUsageUsec(cgroupPath)
	if err != nil {
		logger.Warn("failed to read end cpu usage", "error", err)
	}

	var avgCPUCores float64
	if endCPU > startData.StartCPUUsageUsec && durationSec > 0 {
		deltaUsec := endCPU - startData.StartCPUUsageUsec
		avgCPUCores = float64(deltaUsec) / (float64(durationSec) * 1000000.0)
	}

	peakMem, err := ReadMemoryPeakBytes(cgroupPath)
	if err != nil {
		logger.Warn("failed to read end memory peak", "error", err)
	}

	workerLog := FindLatestWorkerLog()
	displayName := ExtractJobDisplayName(workerLog, os.Getenv("GITHUB_JOB"))

	mv := MetricValues{
		PeakMemoryBytes: peakMem,
		AvgCPUCores:     avgCPUCores,
		DurationSeconds: durationSec,
		JobName:         displayName,
	}
	PopulateMetricValues(&mv)

	logger.Info("job-completed metrics computed",
		"jobName", mv.JobName,
		"durationSec", mv.DurationSeconds,
		"avgCPUCores", mv.AvgCPUCores,
		"peakMemBytes", mv.PeakMemoryBytes,
		"repo", mv.Repository,
	)

	endpoint := os.Getenv("RUNNER_METRICS_ENDPOINT")
	if endpoint == "" {
		logger.Info("RUNNER_METRICS_ENDPOINT is not set, skipping metric export")
		return nil
	}

	payload := BuildOtlpPayload(mv, now)
	if err := SendOtlpMetrics(ctx, endpoint, payload); err != nil {
		logger.Error("failed to export metrics to OTLP endpoint", "endpoint", endpoint, "error", err)
		// We return nil so the hook exit code is 0 and does not fail the runner job
		return nil
	}

	logger.Info("successfully exported metrics to OTLP endpoint", "endpoint", endpoint)
	return nil
}
