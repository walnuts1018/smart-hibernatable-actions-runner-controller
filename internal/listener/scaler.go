package listener

import (
	"context"
	"fmt"
	"strconv"

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

// HandleDesiredRunnerCount handles the desired runner count received from GitHub Actions and updates RunnerScaleSet status.
func (s *ScalerHandler) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	scalerLogger.Info("received desired runner count from GitHub", "count", count)

	var calculatedTarget int32
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var ss ghav1alpha1.RunnerScaleSet
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: s.name}, &ss); err != nil {
			return fmt.Errorf("failed to get RunnerScaleSet: %w", err)
		}

		orig := ss.DeepCopy()

		// Managerが計算したEffectiveMaxRunnersを唯一のsource of truthとして使用
		effectiveMax := ss.Status.EffectiveMaxRunners
		if ss.Spec.Suspend {
			effectiveMax = 0
		}

		targetRunners := min(ss.Spec.Scaling.MinRunners+int32(count), effectiveMax)
		if ss.Spec.Suspend {
			targetRunners = 0
		}

		calculatedTarget = targetRunners
		ss.Status.DesiredRunners = targetRunners
		ss.Status.GitHub.AssignedJobs = int32(count)
		now := metav1.Now()
		ss.Status.Listener.LastPollTime = &now
		ss.Status.Listener.Ready = true

		patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
		return s.client.Status().Patch(ctx, &ss, patch)
	})

	if err != nil {
		scalerLogger.Error(err, "failed to patch desiredRunners on RunnerScaleSet")
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

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var epRunner ghav1alpha1.EphemeralRunner
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: jobInfo.RunnerName}, &epRunner); err != nil {
			return client.IgnoreNotFound(err)
		}

		orig := epRunner.DeepCopy()

		// Terminal状態（Completed, Failed, Deleting）は巻き戻さない（単調遷移）
		switch epRunner.Status.Phase {
		case ghav1alpha1.EphemeralRunnerPhaseCompleted, ghav1alpha1.EphemeralRunnerPhaseFailed, ghav1alpha1.EphemeralRunnerPhaseDeleting:
			return nil
		case ghav1alpha1.EphemeralRunnerPhaseBusy:
			// 既にBusy
		case ghav1alpha1.EphemeralRunnerPhasePending, ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster,
			ghav1alpha1.EphemeralRunnerPhaseProvisioning, ghav1alpha1.EphemeralRunnerPhaseStarting,
			ghav1alpha1.EphemeralRunnerPhaseIdle:
			epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseBusy
		}

		epRunner.Status.GitHub.RunnerID = int64(jobInfo.RunnerID)
		if jobID, err := strconv.ParseInt(jobInfo.JobID, 10, 64); err == nil {
			epRunner.Status.GitHub.JobID = jobID
		}
		patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
		return s.client.Status().Patch(ctx, &epRunner, patch)
	})
}

// HandleJobCompleted handles the notification when a job finishes on a runner.
func (s *ScalerHandler) HandleJobCompleted(ctx context.Context, jobInfo *scaleset.JobCompleted) error {
	if jobInfo == nil || jobInfo.RunnerName == "" {
		return nil
	}
	scalerLogger.Info("job completed notification received from GitHub", "runnerName", jobInfo.RunnerName)

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

		patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
		return s.client.Status().Patch(ctx, &epRunner, patch)
	})
}
