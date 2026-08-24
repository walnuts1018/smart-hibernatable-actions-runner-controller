package githubscaleset

import (
	"testing"

	"github.com/actions/scaleset"
)

func TestMetricsRecorderStoresLatestStatistics(t *testing.T) {
	store := NewStatisticsStore()
	recorder := NewMetricsRecorder("default", "test-scaleset", store)
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
	latest := store.GetLatest()
	if latest == nil {
		t.Fatal("expected non-nil latest statistic")
	}
	if *latest != *stat {
		t.Errorf("unexpected statistic stored in StatisticsStore: %+v", latest)
	}

	recorder.RecordDesiredRunners(10)
	CleanupMetrics("default", "test-scaleset")
}
