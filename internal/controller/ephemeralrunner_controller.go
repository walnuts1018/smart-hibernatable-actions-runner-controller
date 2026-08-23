package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
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
	Recorder        record.EventRecorder
	RemoteProvider  remotecluster.RemoteClusterProvider
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
		if controllerutil.ContainsFinalizer(&epRunner, runner.FinalizerRunnerCleanup) {
			log.Info("cleaning up remote resources for runner", "runner", epRunner.Name)
			var scaleSet ghav1alpha1.RunnerScaleSet
			if err := r.Get(ctx, client.ObjectKey{Namespace: epRunner.Namespace, Name: epRunner.Spec.ScaleSetRef.Name}, &scaleSet); err == nil {
				var nodePool ghav1alpha1.RunnerNodePool
				if err := r.Get(ctx, client.ObjectKey{Namespace: scaleSet.Namespace, Name: scaleSet.Spec.NodePoolRef.Name}, &nodePool); err == nil {
					var cluster ghav1alpha1.RunnerCluster
					if err := r.Get(ctx, client.ObjectKey{Namespace: nodePool.Namespace, Name: nodePool.Spec.ClusterRef.Name}, &cluster); err == nil {
						runnerNs := cluster.Spec.RunnerNamespace
						if runnerNs == "" {
							runnerNs = "gha-runners"
						}
						_, _ = r.cleanupRemoteResources(ctx, &cluster, runnerNs, &epRunner)
					}
				}
			}

			controllerutil.RemoveFinalizer(&epRunner, runner.FinalizerRunnerCleanup)
			if err := r.Update(ctx, &epRunner); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
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
		runnerNs = "gha-runners"
	}

	// 5.Lifecycle state machine
	switch epRunner.Status.Phase {
	case "", ghav1alpha1.EphemeralRunnerPhasePending, ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster:
		// ClusterとNodeのReady確認
		if cluster.Status.Phase != ghav1alpha1.RunnerClusterPhaseReady || !cluster.Status.APIReachable || nodePool.Status.ReadyNodes == 0 {
			epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster
			conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonPending, "Waiting for runner cluster and physical node readiness")
			_ = r.updateStatus(ctx, &epRunner, origRunner)
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
		return r.reconcileRunning(ctx, &epRunner, origRunner, &cluster, runnerNs)

	case ghav1alpha1.EphemeralRunnerPhaseCompleted:
		_, _ = r.cleanupRemoteResources(ctx, &cluster, runnerNs, &epRunner)
		retention := 10 * time.Minute
		if epRunner.Status.FinishedAt != nil {
			elapsed := time.Since(epRunner.Status.FinishedAt.Time)
			if elapsed >= retention {
				log.Info("deleting expired completed ephemeral runner CR", "runner", epRunner.Name)
				if err := r.Delete(ctx, &epRunner); err != nil {
					log.Error(err, "failed to delete expired completed ephemeral runner CR")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
			return ctrl.Result{RequeueAfter: retention - elapsed}, nil
		}
		now := metav1.Now()
		epRunner.Status.FinishedAt = &now
		_ = r.updateStatus(ctx, &epRunner, origRunner)
		return ctrl.Result{RequeueAfter: retention}, nil

	case ghav1alpha1.EphemeralRunnerPhaseFailed:
		_, _ = r.cleanupRemoteResources(ctx, &cluster, runnerNs, &epRunner)
		// Failed CRはトラブルシュート用に1時間保持したのち自動削除 (TTL GC)
		retention := 1 * time.Hour
		if epRunner.Status.FinishedAt != nil {
			elapsed := time.Since(epRunner.Status.FinishedAt.Time)
			if elapsed >= retention {
				log.Info("deleting expired failed ephemeral runner CR", "runner", epRunner.Name)
				if err := r.Delete(ctx, &epRunner); err != nil {
					log.Error(err, "failed to delete expired failed ephemeral runner CR")
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
			return ctrl.Result{RequeueAfter: retention - elapsed}, nil
		}
		if err := r.Delete(ctx, &epRunner); err != nil {
			log.Error(err, "failed to delete failed ephemeral runner CR")
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *EphemeralRunnerReconciler) updateStatus(ctx context.Context, epRunner, orig *ghav1alpha1.EphemeralRunner) error {
	return r.Status().Patch(ctx, epRunner, client.MergeFrom(orig))
}

func (r *EphemeralRunnerReconciler) reconcileProvisioning(ctx context.Context, epRunner, origRunner *ghav1alpha1.EphemeralRunner, scaleSet *ghav1alpha1.RunnerScaleSet, cluster *ghav1alpha1.RunnerCluster, runnerNs string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	remoteClient, err := r.RemoteProvider.GetClient(ctx, cluster)
	if err != nil {
		log.Error(err, "failed to get client for remote cluster")
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, err.Error())
		_ = r.updateStatus(ctx, epRunner, origRunner)
		return ctrl.Result{}, err
	}

	// 1. Remote JIT Secretの存在確認（Checkpoint）
	secretName := runner.JitSecretName(epRunner.Spec.RunnerName)
	var existingSecret corev1.Secret
	err = remoteClient.Get(ctx, client.ObjectKey{Namespace: runnerNs, Name: secretName}, &existingSecret)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to check existing remote JIT secret")
		return ctrl.Result{}, err
	}

	if apierrors.IsNotFound(err) {
		ghaClient, err := r.getGitHubClient(ctx, scaleSet)
		if err != nil {
			log.Error(err, "failed to get github client for JIT config generation")
			conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonFailed, err.Error())
			_ = r.updateStatus(ctx, epRunner, origRunner)
			return ctrl.Result{}, err
		}

		jitConfig, err := ghaClient.GenerateJITConfig(ctx, scaleSet.Status.ScaleSetID, epRunner.Spec.RunnerName, scaleSet.Spec.Runner.WorkDir)
		if err != nil {
			log.Error(err, "failed to generate JIT runner config")
			if r.Recorder != nil {
				r.Recorder.Eventf(epRunner, corev1.EventTypeWarning, "JITConfigFailed", "Failed to generate JIT runner config: %v", err)
			}
			epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseFailed
			conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonFailed, err.Error())
			_ = r.updateStatus(ctx, epRunner, origRunner)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		jitSecret := runner.BuildJitSecret(runnerNs, epRunner, jitConfig)
		if err := remoteClient.Create(ctx, jitSecret); err != nil && !apierrors.IsAlreadyExists(err) {
			log.Error(err, "failed to create JIT secret on remote cluster")
			return ctrl.Result{}, err
		}
	} else {
		// 既存Secretが存在する場合、LabelのRunner UIDを確認して安全性を担保
		if existingSecret.Labels[runner.LabelRunnerUID] != "" && existingSecret.Labels[runner.LabelRunnerUID] != string(epRunner.UID) {
			return ctrl.Result{}, fmt.Errorf("remote JIT secret %s exists with different runner UID", secretName)
		}
	}

	// 2. Remote Runner Podの存在確認
	var existingPod corev1.Pod
	err = remoteClient.Get(ctx, client.ObjectKey{Namespace: runnerNs, Name: epRunner.Spec.RunnerName}, &existingPod)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to check existing remote runner pod")
		return ctrl.Result{}, err
	}

	if apierrors.IsNotFound(err) {
		pod := runner.BuildRunnerPod(runnerNs, scaleSet, epRunner)
		if err := remoteClient.Create(ctx, pod); err != nil && !apierrors.IsAlreadyExists(err) {
			log.Error(err, "failed to create runner pod on remote cluster")
			return ctrl.Result{}, err
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(epRunner, corev1.EventTypeNormal, "Provisioned", "Created remote runner Pod %s and JIT Secret in namespace %s", pod.Name, runnerNs)
		}
	} else {
		if existingPod.Labels[runner.LabelRunnerUID] != "" && existingPod.Labels[runner.LabelRunnerUID] != string(epRunner.UID) {
			return ctrl.Result{}, fmt.Errorf("remote runner pod %s exists with different runner UID", epRunner.Spec.RunnerName)
		}
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

func (r *EphemeralRunnerReconciler) reconcileRunning(ctx context.Context, epRunner, origRunner *ghav1alpha1.EphemeralRunner, cluster *ghav1alpha1.RunnerCluster, runnerNs string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	remoteClient, err := r.RemoteProvider.GetClient(ctx, cluster)
	if err != nil {
		log.Error(err, "failed to get client for remote cluster")
		return ctrl.Result{}, err
	}

	var pod corev1.Pod
	err = remoteClient.Get(ctx, client.ObjectKey{Namespace: runnerNs, Name: epRunner.Spec.RunnerName}, &pod)
	if err != nil {
		if apierrors.IsNotFound(err) {
			now := metav1.Now()
			epRunner.Status.FinishedAt = &now
			if epRunner.Status.GitHub.CompletedObserved {
				log.Info("remote runner pod already deleted after job completion", "runner", epRunner.Name)
				epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseCompleted
				_ = r.updateStatus(ctx, epRunner, origRunner)
				_, _ = r.cleanupRemoteResources(ctx, cluster, runnerNs, epRunner)
				return ctrl.Result{RequeueAfter: 10 * time.Minute}, nil
			}

			log.Info("remote runner pod not found, marking as failed", "runner", epRunner.Name)
			if r.Recorder != nil {
				r.Recorder.Eventf(epRunner, corev1.EventTypeWarning, "PodNotFound", "Remote runner Pod was not found")
			}
			epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseFailed
			epRunner.Status.Failure = &ghav1alpha1.RunnerFailureStatus{
				Reason:  "PodNotFound",
				Message: "Remote runner Pod was unexpectedly not found",
			}
			_ = r.updateStatus(ctx, epRunner, origRunner)
			_, _ = r.cleanupRemoteResources(ctx, cluster, runnerNs, epRunner)
			return ctrl.Result{RequeueAfter: 1 * time.Hour}, nil
		}
		return ctrl.Result{}, err
	}

	epRunner.Status.RemotePod.UID = string(pod.UID)
	epRunner.Status.RemotePod.NodeName = pod.Spec.NodeName

	switch pod.Status.Phase {
	case corev1.PodPending:
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodReady, metav1.ConditionFalse, conditions.ReasonPodPending, "Pod is pending")
	case corev1.PodRunning:
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodReady, metav1.ConditionTrue, conditions.ReasonPodRunning, "Pod is running")
		if epRunner.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseStarting {
			if r.Recorder != nil {
				r.Recorder.Eventf(epRunner, corev1.EventTypeNormal, "PodRunning", "Remote runner Pod is running on node %s", pod.Spec.NodeName)
			}
			epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseIdle
		}
	case corev1.PodSucceeded:
		log.Info("remote runner pod succeeded (job completed)", "runner", epRunner.Name)
		if r.Recorder != nil {
			r.Recorder.Eventf(epRunner, corev1.EventTypeNormal, "JobCompleted", "Remote runner Pod completed successfully")
		}
		now := metav1.Now()
		epRunner.Status.FinishedAt = &now
		epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseCompleted
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodReady, metav1.ConditionFalse, conditions.ReasonPodSucceeded, "Runner job succeeded")
	case corev1.PodFailed:
		log.Info("remote runner pod failed", "runner", epRunner.Name)
		if r.Recorder != nil {
			r.Recorder.Eventf(epRunner, corev1.EventTypeWarning, "JobFailed", "Remote runner Pod failed")
		}
		now := metav1.Now()
		epRunner.Status.FinishedAt = &now
		epRunner.Status.Phase = ghav1alpha1.EphemeralRunnerPhaseFailed
		conditions.SetCondition(&epRunner.Status.Conditions, conditions.TypePodReady, metav1.ConditionFalse, conditions.ReasonPodFailed, "Runner pod failed")

		failureStatus := &ghav1alpha1.RunnerFailureStatus{
			Reason:  "PodFailed",
			Message: "Remote runner Pod failed",
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Terminated != nil {
				failureStatus.Reason = cs.State.Terminated.Reason
				failureStatus.Message = cs.State.Terminated.Message
				failureStatus.ExitCode = cs.State.Terminated.ExitCode
				break
			}
		}
		epRunner.Status.Failure = failureStatus
	}

	if err := r.updateStatus(ctx, epRunner, origRunner); err != nil {
		return ctrl.Result{}, err
	}

	// Completedの場合はリモートリソースを即時削除してCRは10分間TTL保持、Failedの場合は1時間TTL保持
	switch epRunner.Status.Phase {
	case ghav1alpha1.EphemeralRunnerPhaseCompleted:
		_, _ = r.cleanupRemoteResources(ctx, cluster, runnerNs, epRunner)
		return ctrl.Result{RequeueAfter: 10 * time.Minute}, nil
	case ghav1alpha1.EphemeralRunnerPhaseFailed:
		_, _ = r.cleanupRemoteResources(ctx, cluster, runnerNs, epRunner)
		return ctrl.Result{RequeueAfter: 1 * time.Hour}, nil
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
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

func (r *EphemeralRunnerReconciler) cleanupRemoteResources(ctx context.Context, cluster *ghav1alpha1.RunnerCluster, namespace string, epRunner *ghav1alpha1.EphemeralRunner) (bool, error) {
	log := logf.FromContext(ctx)
	remoteClient, err := r.RemoteProvider.GetClient(ctx, cluster)
	if err != nil {
		log.Error(err, "failed to get client for remote cluster cleanup")
		return false, err
	}

	podGone := false
	var pod corev1.Pod
	if err := remoteClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: epRunner.Spec.RunnerName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			podGone = true
		} else {
			return false, err
		}
	} else {
		if pod.DeletionTimestamp.IsZero() {
			if delErr := remoteClient.Delete(ctx, &pod); delErr != nil && !apierrors.IsNotFound(delErr) {
				return false, delErr
			}
		}
	}

	secretGone := false
	var sec corev1.Secret
	if err := remoteClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: runner.JitSecretName(epRunner.Spec.RunnerName)}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			secretGone = true
		} else {
			return false, err
		}
	} else {
		if sec.DeletionTimestamp.IsZero() {
			if delErr := remoteClient.Delete(ctx, &sec); delErr != nil && !apierrors.IsNotFound(delErr) {
				return false, delErr
			}
		}
	}

	return podGone && secretGone, nil
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
				NamespacedName: client.ObjectKey{
					Namespace: run.Namespace,
					Name:      run.Name,
				},
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
				NamespacedName: client.ObjectKey{
					Namespace: run.Namespace,
					Name:      run.Name,
				},
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *EphemeralRunnerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ghav1alpha1.EphemeralRunner{}).
		Watches(&ghav1alpha1.RunnerCluster{}, handler.EnqueueRequestsFromMapFunc(r.findRunnersForCluster)).
		Watches(&ghav1alpha1.RunnerNodePool{}, handler.EnqueueRequestsFromMapFunc(r.findRunnersForNodePool)).
		WithOptions(crcontroller.Options{MaxConcurrentReconciles: 8}).
		Complete(r)
}
