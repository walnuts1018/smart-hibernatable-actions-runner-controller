package listener

import (
	"context"
	"fmt"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/actions/scaleset"
	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
)

var scalerLogger = ctrl.Log.WithName("listener-scaler")

// ScalerHandler implements the GitHub Actions ScaleSet Scaler interface.
type ScalerHandler struct {
	client    client.Client
	namespace string
	name      string
	tracker   *ReadinessTracker
}

// NewScalerHandler creates a new ScalerHandler.
func NewScalerHandler(k8sClient client.Client, namespace, name string, tracker *ReadinessTracker) *ScalerHandler {
	return &ScalerHandler{
		client:    k8sClient,
		namespace: namespace,
		name:      name,
		tracker:   tracker,
	}
}

// HandleDesiredRunnerCount handles the desired runner count received from GitHub Actions and updates EphemeralRunnerSet.
func (s *ScalerHandler) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	scalerLogger.Info("received desired runner count from GitHub", "count", count)

	var calculatedTarget int32
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var ss ghav1alpha1.RunnerScaleSet
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: s.name}, &ss); err != nil {
			return fmt.Errorf("failed to get RunnerScaleSet: %w", err)
		}

		orig := ss.DeepCopy()

		var targetRunners int32
		if ss.Spec.Suspend {
			targetRunners = 0
		} else if ss.Spec.Scaling.MaxRunners != nil {
			targetRunners = min(ss.Spec.Scaling.MinRunners+int32(count), *ss.Spec.Scaling.MaxRunners)
		} else if ss.Status.EffectiveMaxRunners > 0 {
			targetRunners = min(ss.Spec.Scaling.MinRunners+int32(count), ss.Status.EffectiveMaxRunners)
		} else {
			targetRunners = ss.Spec.Scaling.MinRunners + int32(count)
		}

		calculatedTarget = targetRunners
		ss.Status.GitHub.AssignedJobs = int32(count)
		now := metav1.Now()
		ss.Status.Listener.LastPollTime = &now
		ss.Status.Listener.Ready = true

		patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
		if err := s.client.Status().Patch(ctx, &ss, patch); err != nil {
			return err
		}

		// EphemeralRunnerSet.spec.replicas を更新
		var ers ghav1alpha1.EphemeralRunnerSet
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: s.name}, &ers); err == nil {
			origERS := ers.DeepCopy()
			ers.Spec.Replicas = &targetRunners
			patchERS := client.MergeFromWithOptions(origERS, client.MergeFromWithOptimisticLock{})
			_ = s.client.Patch(ctx, &ers, patchERS)
		}

		return nil
	})

	if err != nil {
		scalerLogger.Error(err, "failed to update status/replicas on desired runner count")
		return 0, err
	}

	if s.tracker != nil {
		s.tracker.SetInitialStatisticsReceived(true)
	}

	metrics.DesiredRunners.WithLabelValues(s.namespace, s.name).Set(float64(calculatedTarget))
	return int(calculatedTarget), nil
}

// HandleJobStarted handles the notification when a job starts on a runner.
func (s *ScalerHandler) HandleJobStarted(ctx context.Context, jobInfo *scaleset.JobStarted) error {
	if jobInfo == nil || jobInfo.RunnerName == "" {
		return nil
	}
	scalerLogger.Info("job started on runner", "runnerName", jobInfo.RunnerName)

	if !jobInfo.QueueTime.IsZero() {
		metrics.JobQueueToStartedObservedSeconds.WithLabelValues(s.namespace, s.name).Observe(ctx, time.Since(jobInfo.QueueTime).Seconds())
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var epRunner ghav1alpha1.EphemeralRunner
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: jobInfo.RunnerName}, &epRunner); err != nil {
			return client.IgnoreNotFound(err)
		}

		orig := epRunner.DeepCopy()
		epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseBusy
		epRunner.Status.GitHub.RunnerID = int64(jobInfo.RunnerID)
		if parsedJobID, err := strconv.ParseInt(jobInfo.JobID, 10, 64); err == nil {
			epRunner.Status.GitHub.JobID = parsedJobID
		}
		now := metav1.Now()
		epRunner.Status.GitHub.StartedObservedAt = &now

		patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
		return s.client.Status().Patch(ctx, &epRunner, patch)
	})
}

// HandleJobCompleted handles the notification when a job completes on a runner.
func (s *ScalerHandler) HandleJobCompleted(ctx context.Context, jobInfo *scaleset.JobCompleted) error {
	if jobInfo == nil || jobInfo.RunnerName == "" {
		return nil
	}
	scalerLogger.Info("job completed on runner", "runnerName", jobInfo.RunnerName, "result", jobInfo.Result)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var epRunner ghav1alpha1.EphemeralRunner
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: jobInfo.RunnerName}, &epRunner); err != nil {
			return client.IgnoreNotFound(err)
		}

		orig := epRunner.DeepCopy()
		epRunner.Status.GitHub.CompletedObserved = true
		now := metav1.Now()
		epRunner.Status.GitHub.CompletedObservedAt = &now
		epRunner.Status.GitHub.CompletedResult = jobInfo.Result

		if jobInfo.Result == "success" {
			epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseCompleted
		} else {
			epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseFailed
		}

		patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
		return s.client.Status().Patch(ctx, &epRunner, patch)
	})
}
