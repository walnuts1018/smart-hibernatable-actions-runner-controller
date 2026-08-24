package githubscaleset

import (
	"sync"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
)

// StatisticsStore provides thread-safe in-memory storage for the latest scale set statistics.
type StatisticsStore struct {
	mu     sync.RWMutex
	latest *scaleset.RunnerScaleSetStatistic
}

// NewStatisticsStore creates a new thread-safe StatisticsStore.
func NewStatisticsStore() *StatisticsStore {
	return &StatisticsStore{}
}

// SetLatest stores a snapshot of the latest statistics.
func (s *StatisticsStore) SetLatest(stat *scaleset.RunnerScaleSetStatistic) {
	if stat == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := *stat
	s.latest = &copied
}

// GetLatest retrieves the latest statistics snapshot if available.
func (s *StatisticsStore) GetLatest() *scaleset.RunnerScaleSetStatistic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.latest == nil {
		return nil
	}
	copied := *s.latest
	return &copied
}

type metricsRecorderImpl struct {
	namespace string
	name      string
	store     *StatisticsStore
}

var _ listener.MetricsRecorder = (*metricsRecorderImpl)(nil)

// NewMetricsRecorder creates a listener.MetricsRecorder connected purely to Prometheus metrics and optional StatisticsStore.
func NewMetricsRecorder(namespace, name string, store *StatisticsStore) listener.MetricsRecorder {
	return &metricsRecorderImpl{
		namespace: namespace,
		name:      name,
		store:     store,
	}
}

func (m *metricsRecorderImpl) RecordStatistics(statistics *scaleset.RunnerScaleSetStatistic) {
	if statistics == nil {
		return
	}

	metrics.AvailableJobs.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalAvailableJobs))
	metrics.AcquiredJobs.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalAcquiredJobs))
	metrics.AssignedJobs.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalAssignedJobs))
	metrics.RunningJobs.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalRunningJobs))
	metrics.RegisteredRunners.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalRegisteredRunners))
	metrics.BusyRunners.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalBusyRunners))
	metrics.IdleRunners.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalIdleRunners))

	if m.store != nil {
		m.store.SetLatest(statistics)
	}
}

func (m *metricsRecorderImpl) RecordJobStarted(_ *scaleset.JobStarted) {
	// Job started metrics if needed
}

func (m *metricsRecorderImpl) RecordJobCompleted(_ *scaleset.JobCompleted) {
	// Job completed metrics if needed
}

func (m *metricsRecorderImpl) RecordDesiredRunners(count int) {
	metrics.DesiredRunners.WithLabelValues(m.namespace, m.name).Set(float64(count))
}

// CleanupMetrics deletes Prometheus gauge series for the given scale set.
func CleanupMetrics(namespace, name string) {
	metrics.AvailableJobs.DeleteLabelValues(namespace, name)
	metrics.AcquiredJobs.DeleteLabelValues(namespace, name)
	metrics.AssignedJobs.DeleteLabelValues(namespace, name)
	metrics.RunningJobs.DeleteLabelValues(namespace, name)
	metrics.RegisteredRunners.DeleteLabelValues(namespace, name)
	metrics.BusyRunners.DeleteLabelValues(namespace, name)
	metrics.IdleRunners.DeleteLabelValues(namespace, name)
	metrics.DesiredRunners.DeleteLabelValues(namespace, name)
	metrics.ListenerSessionUp.DeleteLabelValues(namespace, name)
	metrics.ListenerLastSuccessfulPoll.DeleteLabelValues(namespace, name)
}
