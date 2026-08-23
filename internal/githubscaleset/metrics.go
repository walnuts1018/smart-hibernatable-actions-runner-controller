package githubscaleset

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
)

type metricsRecorderImpl struct {
	client    client.Client
	namespace string
	name      string
}

// NewMetricsRecorder creates a listener.MetricsRecorder connected to Prometheus metrics and status updater.
func NewMetricsRecorder(k8sClient client.Client, namespace, name string) listener.MetricsRecorder {
	return &metricsRecorderImpl{
		client:    k8sClient,
		namespace: namespace,
		name:      name,
	}
}

func (m *metricsRecorderImpl) RecordStatistics(statistics *scaleset.RunnerScaleSetStatistic) {
	if statistics == nil {
		return
	}
	metrics.AssignedJobs.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalAssignedJobs))
	metrics.RunningJobs.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalRunningJobs))
	metrics.RegisteredRunners.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalRegisteredRunners))
	metrics.BusyRunners.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalBusyRunners))
	metrics.IdleRunners.WithLabelValues(m.namespace, m.name).Set(float64(statistics.TotalIdleRunners))

	if m.client != nil {
		ctx := context.Background()
		var ss ghav1alpha1.RunnerScaleSet
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: m.namespace, Name: m.name}, &ss); err == nil {
			orig := ss.DeepCopy()
			ss.Status.GitHub.AssignedJobs = int32(statistics.TotalAssignedJobs)
			ss.Status.GitHub.RunningJobs = int32(statistics.TotalRunningJobs)
			ss.Status.GitHub.RegisteredRunners = int32(statistics.TotalRegisteredRunners)
			ss.Status.GitHub.BusyRunners = int32(statistics.TotalBusyRunners)
			ss.Status.GitHub.IdleRunners = int32(statistics.TotalIdleRunners)
			now := metav1.Now()
			ss.Status.GitHub.LastStatisticsTime = &now
			ss.Status.Listener.LastPollTime = &now
			ss.Status.Listener.Ready = true
			m.client.Status().Patch(ctx, &ss, client.MergeFrom(orig))
		}
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
