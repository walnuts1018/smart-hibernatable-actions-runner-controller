package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/conditions"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/githubscaleset"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/remotecluster"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/runner"
)

// EphemeralRunnerReconciler reconciles an EphemeralRunner object.
type EphemeralRunnerReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	Recorder        events.EventRecorder
	RemoteProvider  remotecluster.Provider
	ScaleSetFactory githubscaleset.ScaleSetClientFactory
}

// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=ephemeralrunners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=ephemeralrunners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=ephemeralrunners/finalizers,verbs=update
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnerscalesets,verbs=get;list;watch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnernodepools,verbs=get;list;watch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnerclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *EphemeralRunnerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var epRunner ghav1alpha1.EphemeralRunner
	if err := r.Get(ctx, req.NamespacedName, &epRunner); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	origRunner := epRunner.DeepCopy()

	// 1.Finalizer処理（親リソースが削除済みでも安全にFinalizerを除去できるように先行判定）
	if !epRunner.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, &epRunner)
	}

	// 2.Finalizer確保
	if !controllerutil.ContainsFinalizer(&epRunner, runner.FinalizerRunnerCleanup) {
		controllerutil.AddFinalizer(&epRunner, runner.FinalizerRunnerCleanup)
		if err := r.Update(ctx, &epRunner); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 3.Parent RunnerScaleSetを取得
	var scaleSet ghav1alpha1.RunnerScaleSet
	if err := r.Get(ctx, client.ObjectKey{Namespace: epRunner.Namespace, Name: epRunner.Spec.ScaleSetRef.Name}, &scaleSet); err != nil {
		log.Error(err, "parent RunnerScaleSet not found", "scaleSet", epRunner.Spec.ScaleSetRef.Name)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 4.紐づくRunnerNodePoolとRunnerClusterを取得
	var nodePool ghav1alpha1.RunnerNodePool
	if err := r.Get(ctx, client.ObjectKey{Namespace: scaleSet.Namespace, Name: scaleSet.Spec.NodePoolRef.Name}, &nodePool); err != nil {
		log.Error(err, "referencing RunnerNodePool not found", "nodePool", scaleSet.Spec.NodePoolRef.Name)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	var cluster ghav1alpha1.RunnerCluster
	if err := r.Get(ctx, client.ObjectKey{Namespace: nodePool.Namespace, Name: nodePool.Spec.ClusterRef.Name}, &cluster); err != nil {
		log.Error(err, "referencing RunnerCluster not found", "cluster", nodePool.Spec.ClusterRef.Name)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	runnerNs := cluster.Spec.RunnerNamespace
	if runnerNs == "" {
		runnerNs = runner.DefaultRunnerNamespace
	}

	// 5.Lifecycle state machine
	switch epRunner.Status.Phase {
	case "", ghav1alpha1.EphemeralRunnerPhasePending, ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster:
		// ClusterとNodeのReady確認
		if cluster.Status.Phase != ghav1alpha1.RunnerClusterPhaseReady || !cluster.Status.APIReachable || nodePool.Status.ReadyNodes == 0 {
			epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster
			conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonPending, "Waiting for runner cluster and physical node readiness")
			if updateErr := r.updateStatus(ctx, &epRunner, origRunner); updateErr != nil {
				log.Error(updateErr, "failed to update status")
			}
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		// ClusterがReadyになったのでProvisioningへ
		epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseProvisioning
		if err := r.updateStatus(ctx, &epRunner, origRunner); err != nil {
			return ctrl.Result{}, err
		}
		origRunner = epRunner.DeepCopy()
		fallthrough

	case ghav1alpha1.EphemeralRunnerPhaseProvisioning:
		return r.reconcileProvisioning(ctx, &epRunner, origRunner, &scaleSet, &cluster, runnerNs)

	case ghav1alpha1.EphemeralRunnerPhaseStarting, ghav1alpha1.EphemeralRunnerPhaseIdle, ghav1alpha1.EphemeralRunnerPhaseBusy:
		return r.reconcileRunning(ctx, &epRunner, origRunner, &scaleSet, &cluster, runnerNs)

	case ghav1alpha1.EphemeralRunnerPhaseCompleted, ghav1alpha1.EphemeralRunnerPhaseFailed:
		return r.reconcileTerminal(ctx, &epRunner, origRunner, &cluster, runnerNs)

	case ghav1alpha1.EphemeralRunnerPhaseDeleting:
		return r.reconcileDeletion(ctx, &epRunner)
	}

	return ctrl.Result{}, nil
}

func (r *EphemeralRunnerReconciler) reconcileDeletion(ctx context.Context, epRunner *ghav1alpha1.EphemeralRunner) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if controllerutil.ContainsFinalizer(epRunner, runner.FinalizerRunnerCleanup) {
		// ジョブ実行中 (Busy) で完了未観測の場合はジョブ終了まで待つ
		if epRunner.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseBusy && !epRunner.Status.GitHub.CompletedObserved {
			log.Info("waiting for in-flight job to complete before deleting runner", "runner", epRunner.Name)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		log.Info("cleaning up resources for runner", "runner", epRunner.Name)
		var scaleSet ghav1alpha1.RunnerScaleSet
		if err := r.Get(ctx, client.ObjectKey{Namespace: epRunner.Namespace, Name: epRunner.Spec.ScaleSetRef.Name}, &scaleSet); err == nil {
			// 1. GitHub Actions 上の Runner 登録を解除 (Scale-down 時の孤立防止)
			runnerID := int64(0)
			if epRunner.Status.Provisioning != nil && epRunner.Status.Provisioning.RunnerID != 0 {
				runnerID = epRunner.Status.Provisioning.RunnerID
			} else if epRunner.Status.GitHub.RunnerID != 0 {
				runnerID = epRunner.Status.GitHub.RunnerID
			}

			if runnerID != 0 && !epRunner.Status.GitHub.CompletedObserved {
				if ghaClient, clientErr := r.getGitHubClient(ctx, &scaleSet); clientErr == nil {
					log.Info("removing runner registration from GitHub Actions during deletion", "runnerID", runnerID, "runner", epRunner.Name)
					if removeErr := ghaClient.RemoveRunner(context.WithoutCancel(ctx), runnerID); removeErr != nil {
						log.Error(removeErr, "failed to remove runner from GitHub Actions (proceeding with remote cleanup)")
					}
				}
			}

			// 2. リモートクラスタの Pod/Secret をクリーンアップ
			var nodePool ghav1alpha1.RunnerNodePool
			if err := r.Get(ctx, client.ObjectKey{Namespace: scaleSet.Namespace, Name: scaleSet.Spec.NodePoolRef.Name}, &nodePool); err == nil {
				var cluster ghav1alpha1.RunnerCluster
				if err := r.Get(ctx, client.ObjectKey{Namespace: nodePool.Namespace, Name: nodePool.Spec.ClusterRef.Name}, &cluster); err == nil {
					runnerNs := cluster.Spec.RunnerNamespace
					if runnerNs == "" {
						runnerNs = runner.DefaultRunnerNamespace
					}
					if cleanupErr := r.cleanupRemoteResources(ctx, &cluster, runnerNs, epRunner); cleanupErr != nil {
						log.Error(cleanupErr, "failed to cleanup remote resources during deletion")
					}
				}
			}
		}

		controllerutil.RemoveFinalizer(epRunner, runner.FinalizerRunnerCleanup)
		if err := r.Update(ctx, epRunner); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *EphemeralRunnerReconciler) updateStatus(ctx context.Context, epRunner, _ *ghav1alpha1.EphemeralRunner) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current ghav1alpha1.EphemeralRunner
		if err := r.Get(ctx, client.ObjectKeyFromObject(epRunner), &current); err != nil {
			return err
		}
		orig := current.DeepCopy()
		current.Status = epRunner.Status
		patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
		return r.Status().Patch(ctx, &current, patch)
	})
}

func jitBackoff(failures int32) time.Duration {
	b := wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   2.0,
		Cap:      60 * time.Second,
		Steps:    int(failures),
	}
	dur := b.Duration
	for range int(failures) {
		dur = b.Step()
	}
	return dur
}

func (r *EphemeralRunnerReconciler) reconcileProvisioning(ctx context.Context, epRunner, origRunner *ghav1alpha1.EphemeralRunner, scaleSet *ghav1alpha1.RunnerScaleSet, cluster *ghav1alpha1.RunnerCluster, runnerNs string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 0. ScaleSetID == 0 ガード (依存リソース待機: JIT API は呼ばず Requeue)
	if scaleSet.Status.ScaleSetID == 0 {
		log.Info("waiting for RunnerScaleSet to register with GitHub Actions (scaleSetID is 0)", "scaleSet", scaleSet.Name)
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, "WaitingForScaleSet", "RunnerScaleSet is not registered in GitHub Actions yet")
		if updateErr := r.updateStatus(ctx, epRunner, origRunner); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// 1. Attempt identity の事前永続化 (JIT API 呼び出し前に Status に記録)
	if epRunner.Status.Provisioning == nil || epRunner.Status.Provisioning.RunnerName == "" {
		now := metav1.Now()
		epRunner.Status.Provisioning = &ghav1alpha1.ProvisioningAttemptStatus{
			ID:         fmt.Sprintf("att-%d", now.UnixNano()),
			RunnerName: epRunner.Spec.RunnerName,
			StartedAt:  &now,
		}
		if err := r.updateStatus(ctx, epRunner, origRunner); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	// リトライ待機時間 (NextRetryAt) の確認
	if epRunner.Status.Provisioning.NextRetryAt != nil && time.Now().Before(epRunner.Status.Provisioning.NextRetryAt.Time) {
		remaining := time.Until(epRunner.Status.Provisioning.NextRetryAt.Time)
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	remoteClient, err := r.RemoteProvider.GetClient(ctx, cluster)
	if err != nil {
		log.Error(err, "failed to get client for remote cluster")
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, err.Error())
		if updateErr := r.updateStatus(ctx, epRunner, origRunner); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// 2. Remote JIT Secret の存在確認（Checkpoint）
	jitRes, err := r.ensureJitSecret(ctx, remoteClient, epRunner, origRunner, scaleSet, runnerNs)
	if err != nil {
		return ctrl.Result{}, err
	}
	if jitRes != nil {
		return *jitRes, nil
	}

	// 3. Remote Runner Podの存在確認
	podRes, err := r.ensureRunnerPod(ctx, remoteClient, epRunner, scaleSet, runnerNs)
	if err != nil {
		return ctrl.Result{}, err
	}
	if podRes != nil {
		return *podRes, nil
	}

	epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseStarting
	epRunner.Status.RemotePod = ghav1alpha1.RemotePodStatus{
		Namespace: runnerNs,
		Name:      epRunner.Spec.RunnerName,
	}
	conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodCreated, metav1.ConditionTrue, conditions.ReasonSuccess, "Remote Pod and Secret created")
	conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonReady, "Runner pod starting")

	if err := r.updateStatus(ctx, epRunner, origRunner); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *EphemeralRunnerReconciler) ensureJitSecret(
	ctx context.Context,
	remoteClient client.Client,
	epRunner, origRunner *ghav1alpha1.EphemeralRunner,
	scaleSet *ghav1alpha1.RunnerScaleSet,
	runnerNs string,
) (*ctrl.Result, error) {
	log := logf.FromContext(ctx)
	secretName := runner.JitSecretName(epRunner.Spec.RunnerName)
	var existingSecret corev1.Secret
	err := remoteClient.Get(ctx, client.ObjectKey{Namespace: runnerNs, Name: secretName}, &existingSecret)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to check existing remote JIT secret")
		res := ctrl.Result{RequeueAfter: 5 * time.Second}
		return &res, nil
	}

	if apierrors.IsNotFound(err) {
		ghaClient, clientErr := r.getGitHubClient(ctx, scaleSet)
		if clientErr != nil {
			log.Error(clientErr, "failed to get github client for JIT config generation")
			conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonFailed, clientErr.Error())
			if updateErr := r.updateStatus(ctx, epRunner, origRunner); updateErr != nil {
				log.Error(updateErr, "failed to update status")
			}
			res := ctrl.Result{RequeueAfter: 10 * time.Second}
			return &res, nil
		}

		existingRunner, getErr := ghaClient.GetRunnerByName(ctx, epRunner.Status.Provisioning.RunnerName)
		if getErr == nil && existingRunner != nil {
			log.Info("found existing orphaned runner on GitHub from previous attempt, cleaning up", "runnerName", epRunner.Status.Provisioning.RunnerName, "runnerID", existingRunner.ID)
			if removeErr := ghaClient.RemoveRunner(ctx, int64(existingRunner.ID)); removeErr != nil {
				log.Error(removeErr, "failed to remove existing orphaned runner")
			}
		}

		jitResp, genErr := ghaClient.GenerateJITConfig(ctx, scaleSet.Status.ScaleSetID, epRunner.Status.Provisioning.RunnerName, scaleSet.Spec.Runner.WorkDir)
		if genErr != nil {
			log.Error(genErr, "failed to generate JIT runner config, checking for ambiguous creation", "runnerName", epRunner.Status.Provisioning.RunnerName)

			// Ambiguous failure チェック: タイムアウト等でGitHub側にRunnerが作成されていないか確認
			if checkRunner, checkErr := ghaClient.GetRunnerByName(ctx, epRunner.Status.Provisioning.RunnerName); checkErr == nil && checkRunner != nil {
				log.Info("found runner created on GitHub after ambiguous GenerateJITConfig failure, cleaning up for next attempt",
					"runnerName", epRunner.Status.Provisioning.RunnerName, "runnerID", checkRunner.ID)
				_ = ghaClient.RemoveRunner(context.WithoutCancel(ctx), int64(checkRunner.ID))
			}

			epRunner.Status.Provisioning.Failures++
			backoff := jitBackoff(epRunner.Status.Provisioning.Failures)
			nextRetry := metav1.NewTime(time.Now().Add(backoff))
			epRunner.Status.Provisioning.NextRetryAt = &nextRetry

			if r.Recorder != nil {
				r.Recorder.Eventf(epRunner, nil, corev1.EventTypeWarning, "JITConfigRetry", "Reconcile", "Failed to generate JIT config (attempt %d): %v, retrying in %s", epRunner.Status.Provisioning.Failures, genErr, backoff)
			}

			// Phase は Provisioning のまま維持（Failed に倒さずリトライ）
			conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, "JITGenerationRetrying", fmt.Sprintf("Failed to generate JIT config (failure %d): %v", epRunner.Status.Provisioning.Failures, genErr))
			if updateErr := r.updateStatus(ctx, epRunner, origRunner); updateErr != nil {
				log.Error(updateErr, "failed to update status")
			}
			res := ctrl.Result{RequeueAfter: backoff}
			return &res, nil
		}

		now := metav1.Now()
		epRunner.Status.Provisioning.RunnerID = jitResp.RunnerID
		epRunner.Status.Provisioning.JITGeneratedAt = &now
		epRunner.Status.Provisioning.Failures = 0
		epRunner.Status.Provisioning.NextRetryAt = nil

		jitSecret := runner.BuildJitSecret(runnerNs, epRunner, jitResp.EncodedJITConfig)
		if createErr := remoteClient.Create(ctx, jitSecret); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
			log.Error(createErr, "failed to create JIT secret on remote cluster; removing runner from GitHub as compensation")
			if jitResp.RunnerID != 0 {
				if removeErr := ghaClient.RemoveRunner(context.WithoutCancel(ctx), jitResp.RunnerID); removeErr != nil {
					log.Error(removeErr, "failed to remove runner compensation")
				}
			}
			res := ctrl.Result{RequeueAfter: 5 * time.Second}
			return &res, nil
		}
	} else {
		if existingSecret.Labels[runner.LabelRunnerUID] != "" && existingSecret.Labels[runner.LabelRunnerUID] != string(epRunner.UID) {
			return nil, fmt.Errorf("remote JIT secret %s exists with different runner UID", secretName)
		}
	}

	return nil, nil
}

func (r *EphemeralRunnerReconciler) ensureRunnerPod(
	ctx context.Context,
	remoteClient client.Client,
	epRunner *ghav1alpha1.EphemeralRunner,
	scaleSet *ghav1alpha1.RunnerScaleSet,
	runnerNs string,
) (*ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var existingPod corev1.Pod
	err := remoteClient.Get(ctx, client.ObjectKey{Namespace: runnerNs, Name: epRunner.Spec.RunnerName}, &existingPod)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to check existing remote runner pod")
		res := ctrl.Result{RequeueAfter: 5 * time.Second}
		return &res, nil
	}

	if apierrors.IsNotFound(err) {
		pod := runner.BuildRunnerPod(runnerNs, scaleSet, epRunner)
		if createErr := remoteClient.Create(ctx, pod); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
			log.Error(createErr, "failed to create runner pod on remote cluster")
			res := ctrl.Result{RequeueAfter: 5 * time.Second}
			return &res, nil
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(epRunner, nil, corev1.EventTypeNormal, "Provisioned", "Reconcile", "Created remote runner Pod %s and JIT Secret in namespace %s", pod.Name, runnerNs)
		}
	} else {
		if existingPod.Labels[runner.LabelRunnerUID] != "" && existingPod.Labels[runner.LabelRunnerUID] != string(epRunner.UID) {
			return nil, fmt.Errorf("remote runner pod %s exists with different runner UID", epRunner.Spec.RunnerName)
		}
	}

	return nil, nil
}

func (r *EphemeralRunnerReconciler) reconcileRunning(ctx context.Context, epRunner, origRunner *ghav1alpha1.EphemeralRunner, scaleSet *ghav1alpha1.RunnerScaleSet, cluster *ghav1alpha1.RunnerCluster, runnerNs string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	remoteClient, err := r.RemoteProvider.GetClient(ctx, cluster)
	if err != nil {
		log.Error(err, "failed to get client for remote cluster")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	var pod corev1.Pod
	err = remoteClient.Get(ctx, client.ObjectKey{Namespace: runnerNs, Name: epRunner.Spec.RunnerName}, &pod)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return r.handlePodNotFound(ctx, epRunner, origRunner, scaleSet, cluster, runnerNs)
		}
		// 一時的な通信エラーの場合はFailedに倒さずRequeue
		log.Error(err, "transient error reading remote pod, requeueing", "runner", epRunner.Name)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	epRunner.Status.RemotePod.UID = string(pod.UID)
	epRunner.Status.RemotePod.NodeName = pod.Spec.NodeName

	r.applyPodStatus(epRunner, &pod)

	if err := r.updateStatus(ctx, epRunner, origRunner); err != nil {
		return ctrl.Result{}, err
	}

	// Completedの場合はリモートリソースを即時削除してCRは10分間TTL保持、Failedの場合は1時間TTL保持
	switch epRunner.Status.Phase {
	case ghav1alpha1.EphemeralRunnerPhaseCompleted, ghav1alpha1.EphemeralRunnerPhaseFailed:
		return r.reconcileTerminal(ctx, epRunner, origRunner, cluster, runnerNs)
	case ghav1alpha1.EphemeralRunnerPhasePending,
		ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster,
		ghav1alpha1.EphemeralRunnerPhaseProvisioning,
		ghav1alpha1.EphemeralRunnerPhaseStarting,
		ghav1alpha1.EphemeralRunnerPhaseIdle,
		ghav1alpha1.EphemeralRunnerPhaseBusy,
		ghav1alpha1.EphemeralRunnerPhaseDeleting:
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func getPodTerminatedTime(pod *corev1.Pod) *time.Time {
	if pod == nil {
		return nil
	}
	var latest time.Time
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil && !cs.State.Terminated.FinishedAt.IsZero() {
			t := cs.State.Terminated.FinishedAt.Time
			if t.After(latest) {
				latest = t
			}
		}
	}
	if !latest.IsZero() {
		return &latest
	}
	return nil
}

func markTerminal(epRunner *ghav1alpha1.EphemeralRunner, phase ghav1alpha1.EphemeralRunnerPhase, retention time.Duration, actualTerminatedAt *time.Time) {
	epRunner.Status.Phase = phase
	if epRunner.Status.FinishedAt == nil {
		fin := time.Now()
		if actualTerminatedAt != nil && !actualTerminatedAt.IsZero() {
			fin = *actualTerminatedAt
		}
		finMeta := metav1.NewTime(fin)
		epRunner.Status.FinishedAt = &finMeta
		gcAt := metav1.NewTime(fin.Add(retention))
		epRunner.Status.GCEligibleAt = &gcAt
	}
}

func (r *EphemeralRunnerReconciler) reconcileTerminal(
	ctx context.Context,
	epRunner, origRunner *ghav1alpha1.EphemeralRunner,
	cluster *ghav1alpha1.RunnerCluster,
	runnerNs string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// リモートリソース（Pod, Secret）のクリーンアップ
	if cleanupErr := r.cleanupRemoteResources(ctx, cluster, runnerNs, epRunner); cleanupErr != nil {
		log.Error(cleanupErr, "failed to cleanup remote resources during terminal reconciliation")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if epRunner.Status.GCEligibleAt == nil {
		retention := 10 * time.Minute
		if epRunner.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseFailed {
			retention = 1 * time.Hour
		}
		markTerminal(epRunner, epRunner.Status.Phase, retention, nil)
		if err := r.updateStatus(ctx, epRunner, origRunner); err != nil {
			return ctrl.Result{}, err
		}
	}

	remaining := time.Until(epRunner.Status.GCEligibleAt.Time)
	if remaining <= 0 {
		log.Info("garbage collecting finished EphemeralRunner CR", "runner", epRunner.Name, "phase", epRunner.Status.Phase)
		if err := r.Delete(ctx, epRunner); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "failed to delete finished EphemeralRunner CR")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: remaining}, nil
}

func (r *EphemeralRunnerReconciler) handlePodNotFound(
	ctx context.Context,
	epRunner, origRunner *ghav1alpha1.EphemeralRunner,
	scaleSet *ghav1alpha1.RunnerScaleSet,
	cluster *ghav1alpha1.RunnerCluster,
	runnerNs string,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if epRunner.Status.GitHub.CompletedObserved {
		log.Info("remote runner pod already deleted after job completion", "runner", epRunner.Name)
		markTerminal(epRunner, ghav1alpha1.EphemeralRunnerPhaseCompleted, 10*time.Minute, nil)
		if updateErr := r.updateStatus(ctx, epRunner, origRunner); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return r.reconcileTerminal(ctx, epRunner, origRunner, cluster, runnerNs)
	}

	log.Info("remote runner pod not found, marking as failed", "runner", epRunner.Name)
	if r.Recorder != nil {
		r.Recorder.Eventf(epRunner, nil, corev1.EventTypeWarning, "PodNotFound", "Reconcile", "Remote runner Pod was not found")
	}
	markTerminal(epRunner, ghav1alpha1.EphemeralRunnerPhaseFailed, 1*time.Hour, nil)
	epRunner.Status.Failure = &ghav1alpha1.RunnerFailureStatus{
		Reason:  "PodNotFound",
		Message: "Remote runner Pod was unexpectedly not found",
	}

	// Job完了前にPodが消滅した場合、GitHub側RunnerをBest-effortでRemove
	if !epRunner.Status.GitHub.CompletedObserved && epRunner.Status.Provisioning != nil && epRunner.Status.Provisioning.RunnerID != 0 {
		if ghaClient, clientErr := r.getGitHubClient(ctx, scaleSet); clientErr == nil {
			if removeErr := ghaClient.RemoveRunner(context.WithoutCancel(ctx), epRunner.Status.Provisioning.RunnerID); removeErr != nil {
				log.Error(removeErr, "failed to remove runner from GitHub")
			}
		}
	}

	if updateErr := r.updateStatus(ctx, epRunner, origRunner); updateErr != nil {
		log.Error(updateErr, "failed to update status")
	}
	return r.reconcileTerminal(ctx, epRunner, origRunner, cluster, runnerNs)
}

type podStartupState string

const (
	podStartupScheduling       podStartupState = "Scheduling"
	podStartupStarting         podStartupState = "Starting"
	podStartupRunning          podStartupState = "Running"
	podStartupCompleted        podStartupState = "Completed"
	podStartupRetryableFailure podStartupState = "RetryableFailure"
	podStartupTerminalFailure  podStartupState = "TerminalFailure"
)

type podObservation struct {
	State    podStartupState
	Reason   string
	Message  string
	ExitCode int32
}

func observeRunnerPod(pod *corev1.Pod) podObservation {
	if pod == nil {
		return podObservation{State: podStartupTerminalFailure, Reason: "PodNotFound", Message: "Pod is nil"}
	}

	// 1. Succeeded / Failed Phases
	if pod.Status.Phase == corev1.PodSucceeded {
		return podObservation{State: podStartupCompleted, Reason: "PodSucceeded", Message: "Runner pod succeeded"}
	}
	if pod.Status.Phase == corev1.PodFailed {
		reason := "PodFailed"
		msg := pod.Status.Message
		var exitCode int32
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Terminated != nil {
				reason = cs.State.Terminated.Reason
				msg = cs.State.Terminated.Message
				exitCode = cs.State.Terminated.ExitCode
				break
			}
		}
		return podObservation{State: podStartupTerminalFailure, Reason: reason, Message: msg, ExitCode: exitCode}
	}

	// 2. Container Status Inspection
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "ImagePullBackOff", "ErrImagePull":
				return podObservation{State: podStartupRetryableFailure, Reason: cs.State.Waiting.Reason, Message: cs.State.Waiting.Message}
			case "InvalidImageName", "CreateContainerConfigError", "CreateContainerError":
				return podObservation{State: podStartupTerminalFailure, Reason: cs.State.Waiting.Reason, Message: cs.State.Waiting.Message}
			}
		}
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
			return podObservation{
				State:    podStartupTerminalFailure,
				Reason:   cs.State.Terminated.Reason,
				Message:  cs.State.Terminated.Message,
				ExitCode: cs.State.Terminated.ExitCode,
			}
		}
	}

	// 3. Running Phase
	if pod.Status.Phase == corev1.PodRunning {
		return podObservation{State: podStartupRunning, Reason: "PodRunning", Message: "Runner pod is running"}
	}

	// 4. Pending Phase (Scheduling check)
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Reason == corev1.PodReasonUnschedulable {
			return podObservation{State: podStartupScheduling, Reason: cond.Reason, Message: cond.Message}
		}
	}

	return podObservation{State: podStartupStarting, Reason: "PodPending", Message: "Runner pod is starting"}
}

func (r *EphemeralRunnerReconciler) applyPodStatus(epRunner *ghav1alpha1.EphemeralRunner, pod *corev1.Pod) {
	log := logf.Log.WithName("ephemeralrunner-controller")
	obs := observeRunnerPod(pod)

	switch obs.State {
	case podStartupScheduling:
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodReady, metav1.ConditionFalse, obs.Reason, obs.Message)
	case podStartupStarting:
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodReady, metav1.ConditionFalse, obs.Reason, obs.Message)
	case podStartupRetryableFailure:
		log.Info("runner pod in retryable failure state", "runner", epRunner.Name, "reason", obs.Reason)
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodReady, metav1.ConditionFalse, obs.Reason, obs.Message)
	case podStartupRunning:
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodReady, metav1.ConditionTrue, conditions.ReasonPodRunning, "Pod is running")
		if epRunner.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseStarting {
			if r.Recorder != nil {
				r.Recorder.Eventf(epRunner, nil, corev1.EventTypeNormal, "PodRunning", "Reconcile", "Remote runner Pod is running on node %s", pod.Spec.NodeName)
			}
			epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseIdle
		}
	case podStartupCompleted:
		log.Info("remote runner pod succeeded (job completed)", "runner", epRunner.Name)
		if r.Recorder != nil {
			r.Recorder.Eventf(epRunner, nil, corev1.EventTypeNormal, "JobCompleted", "Reconcile", "Remote runner Pod completed successfully")
		}
		markTerminal(epRunner, ghav1alpha1.EphemeralRunnerPhaseCompleted, 10*time.Minute, getPodTerminatedTime(pod))
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodReady, metav1.ConditionFalse, conditions.ReasonPodSucceeded, "Runner job succeeded")
	case podStartupTerminalFailure:
		log.Info("remote runner pod terminal failure", "runner", epRunner.Name, "reason", obs.Reason)
		if r.Recorder != nil {
			r.Recorder.Eventf(epRunner, nil, corev1.EventTypeWarning, "JobFailed", "Reconcile", "Remote runner Pod failed: %s", obs.Message)
		}
		markTerminal(epRunner, ghav1alpha1.EphemeralRunnerPhaseFailed, 1*time.Hour, getPodTerminatedTime(pod))
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodReady, metav1.ConditionFalse, obs.Reason, obs.Message)
		epRunner.Status.Failure = &ghav1alpha1.RunnerFailureStatus{
			Reason:   obs.Reason,
			Message:  obs.Message,
			ExitCode: obs.ExitCode,
		}
	}
}

func (r *EphemeralRunnerReconciler) getGitHubClient(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet) (githubscaleset.ScaleSetClient, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: ss.Spec.GitHub.CredentialsSecretRef.Name}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get github credentials secret: %w", err)
	}

	auth, err := githubscaleset.ParseGitHubAppAuth(secret.Data)
	if err != nil {
		return nil, err
	}

	return r.ScaleSetFactory.NewClient(ss.Spec.GitHub.ConfigURL, auth)
}

func (r *EphemeralRunnerReconciler) cleanupRemoteResources(ctx context.Context, cluster *ghav1alpha1.RunnerCluster, namespace string, epRunner *ghav1alpha1.EphemeralRunner) error {
	log := logf.FromContext(ctx)
	remoteClient, err := r.RemoteProvider.GetClient(ctx, cluster)
	if err != nil {
		log.Error(err, "failed to get client for remote cluster cleanup")
		return err
	}

	var pod corev1.Pod
	if err := remoteClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: epRunner.Spec.RunnerName}, &pod); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	} else {
		if pod.DeletionTimestamp.IsZero() {
			if delErr := remoteClient.Delete(ctx, &pod); delErr != nil && !apierrors.IsNotFound(delErr) {
				return delErr
			}
		}
	}

	var sec corev1.Secret
	if err := remoteClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: runner.JitSecretName(epRunner.Spec.RunnerName)}, &sec); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	} else {
		if sec.DeletionTimestamp.IsZero() {
			if delErr := remoteClient.Delete(ctx, &sec); delErr != nil && !apierrors.IsNotFound(delErr) {
				return delErr
			}
		}
	}

	return nil
}

func (r *EphemeralRunnerReconciler) findRunnersForCluster(ctx context.Context, obj client.Object) []ctrl.Request {
	cluster, ok := obj.(*ghav1alpha1.RunnerCluster)
	if !ok {
		return nil
	}

	var runners ghav1alpha1.EphemeralRunnerList
	if err := r.List(ctx, &runners, client.InNamespace(cluster.Namespace)); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, run := range runners.Items {
		if run.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster || run.Status.Phase == ghav1alpha1.EphemeralRunnerPhasePending {
			requests = append(requests, ctrl.Request{
				Namespace: run.Namespace,
				Name:      run.Name,
			})
		}
	}
	return requests
}

func (r *EphemeralRunnerReconciler) findRunnersForNodePool(ctx context.Context, obj client.Object) []ctrl.Request {
	pool, ok := obj.(*ghav1alpha1.RunnerNodePool)
	if !ok {
		return nil
	}

	var runners ghav1alpha1.EphemeralRunnerList
	if err := r.List(ctx, &runners, client.InNamespace(pool.Namespace)); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, run := range runners.Items {
		if run.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster || run.Status.Phase == ghav1alpha1.EphemeralRunnerPhasePending {
			requests = append(requests, ctrl.Request{
				Namespace: run.Namespace,
				Name:      run.Name,
			})
		}
	}
	return requests
}

func (r *EphemeralRunnerReconciler) findRunnersForScaleSet(ctx context.Context, obj client.Object) []ctrl.Request {
	ss, ok := obj.(*ghav1alpha1.RunnerScaleSet)
	if !ok {
		return nil
	}

	var runners ghav1alpha1.EphemeralRunnerList
	if err := r.List(ctx, &runners, client.InNamespace(ss.Namespace)); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, run := range runners.Items {
		matches := false
		if run.Labels[runner.LabelScaleSetUID] != "" {
			matches = run.Labels[runner.LabelScaleSetUID] == string(ss.UID)
		} else {
			matches = run.Spec.ScaleSetRef.Name == ss.Name
		}
		if matches && (run.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster ||
			run.Status.Phase == ghav1alpha1.EphemeralRunnerPhasePending ||
			run.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseProvisioning) {
			requests = append(requests, ctrl.Request{
				Namespace: run.Namespace,
				Name:      run.Name,
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *EphemeralRunnerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ghav1alpha1.EphemeralRunner{}).
		Watches(&ghav1alpha1.RunnerScaleSet{}, handler.EnqueueRequestsFromMapFunc(r.findRunnersForScaleSet)).
		Watches(&ghav1alpha1.RunnerCluster{}, handler.EnqueueRequestsFromMapFunc(r.findRunnersForCluster)).
		Watches(&ghav1alpha1.RunnerNodePool{}, handler.EnqueueRequestsFromMapFunc(r.findRunnersForNodePool)).
		WithOptions(crcontroller.Options{MaxConcurrentReconciles: 8}).
		Complete(r)
}
