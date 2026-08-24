package githubscaleset

import (
	"testing"

	"github.com/actions/scaleset"
	dto "github.com/prometheus/client_model/go"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
)

func getGaugeValue(t *testing.T, metric *dto.Metric) float64 {
	t.Helper()
	if metric == nil || metric.Gauge == nil {
		t.Fatalf("metric or gauge is nil")
	}
	return metric.Gauge.GetValue()
}

func TestMetricsRecorderAndStore(t *testing.T) {
	namespace := "default"
	name := "test-scaleset"

	store := NewStatisticsStore()
	recorder := NewMetricsRecorder(namespace, name, store)

	stat := &scaleset.RunnerScaleSetStatistic{
		TotalAvailableJobs:     1,
		TotalAcquiredJobs:      2,
		TotalAssignedJobs:      3,
		TotalRunningJobs:       4,
		TotalRegisteredRunners: 5,
		TotalBusyRunners:       6,
		TotalIdleRunners:       7,
	}

	recorder.RecordStatistics(stat)

	// Check StatisticsStore
	latest := store.GetLatest()
	if latest == nil {
		t.Fatalf("expected non-nil latest statistic")
	}
	if latest.TotalAvailableJobs != 1 || latest.TotalAcquiredJobs != 2 || latest.TotalAssignedJobs != 3 ||
		latest.TotalRunningJobs != 4 || latest.TotalRegisteredRunners != 5 || latest.TotalBusyRunners != 6 ||
		latest.TotalIdleRunners != 7 {
		t.Errorf("unexpected statistic stored in StatisticsStore: %+v", latest)
	}

	// Check Prometheus metrics
	var m dto.Metric

	if err := metrics.AvailableJobs.WithLabelValues(namespace, name).Write(&m); err != nil {
		t.Fatalf("failed to read AvailableJobs metric: %v", err)
	}
	if getGaugeValue(t, &m) != 1 {
		t.Errorf("expected AvailableJobs=1, got %v", getGaugeValue(t, &m))
	}

	if err := metrics.AcquiredJobs.WithLabelValues(namespace, name).Write(&m); err != nil {
		t.Fatalf("failed to read AcquiredJobs metric: %v", err)
	}
	if getGaugeValue(t, &m) != 2 {
		t.Errorf("expected AcquiredJobs=2, got %v", getGaugeValue(t, &m))
	}

	if err := metrics.AssignedJobs.WithLabelValues(namespace, name).Write(&m); err != nil {
		t.Fatalf("failed to read AssignedJobs metric: %v", err)
	}
	if getGaugeValue(t, &m) != 3 {
		t.Errorf("expected AssignedJobs=3, got %v", getGaugeValue(t, &m))
	}

	recorder.RecordDesiredRunners(10)
	if err := metrics.DesiredRunners.WithLabelValues(namespace, name).Write(&m); err != nil {
		t.Fatalf("failed to read DesiredRunners metric: %v", err)
	}
	if getGaugeValue(t, &m) != 10 {
		t.Errorf("expected DesiredRunners=10, got %v", getGaugeValue(t, &m))
	}

	// Cleanup
	CleanupMetrics(namespace, name)
}
