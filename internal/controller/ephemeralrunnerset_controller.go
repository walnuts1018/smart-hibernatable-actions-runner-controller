package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/conditions"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/runner"
)

// EphemeralRunnerSetReconciler reconciles an EphemeralRunnerSet object.
type EphemeralRunnerSetReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=ephemeralrunnersets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=ephemeralrunnersets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=ephemeralrunnersets/finalizers,verbs=update
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=ephemeralrunners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=ephemeralrunners/status,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *EphemeralRunnerSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var ers ghav1alpha1.EphemeralRunnerSet
	if err := r.Get(ctx, req.NamespacedName, &ers); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	origERS := ers.DeepCopy()
	ers.Status.ObservedGeneration = ers.Generation

	// 1. 配下の EphemeralRunner 一覧を取得
	var runnerList ghav1alpha1.EphemeralRunnerList
	if err := r.List(ctx, &runnerList, client.InNamespace(ers.Namespace), client.MatchingFields{
		IndexScaleSetRefName: ers.Spec.ScaleSetRef.Name,
	}); err != nil {
		if err := r.List(ctx, &runnerList, client.InNamespace(ers.Namespace)); err != nil {
			return ctrl.Result{}, err
		}
	}

	var activeRunners []*ghav1alpha1.EphemeralRunner
	for i := range runnerList.Items {
		run := &runnerList.Items[i]
		if run.Spec.ScaleSetRef.Name == ers.Spec.ScaleSetRef.Name && isRunnerNonTerminal(run.Status.Phase) {
			activeRunners = append(activeRunners, run)
		}
	}

	ers.Status.ActiveReplicas = int32(len(activeRunners))

	targetReplicas := int32(0)
	if ers.Spec.Replicas != nil {
		targetReplicas = *ers.Spec.Replicas
	}

	// 2. スケールアップ (target > active)
	if targetReplicas > int32(len(activeRunners)) {
		diff := targetReplicas - int32(len(activeRunners))
		log.Info("scaling up EphemeralRunners", "count", diff, "scaleSet", ers.Spec.ScaleSetRef.Name)
		if r.Recorder != nil {
			r.Recorder.Eventf(&ers, nil, corev1.EventTypeNormal, "ScalingUp", "Reconcile", "Scaling up %d ephemeral runner(s) (target: %d, active: %d)", diff, targetReplicas, len(activeRunners))
		}
		for range diff {
			runnerName := runner.GenerateRunnerName(ers.Spec.ScaleSetRef.Name)
			newRunner := &ghav1alpha1.EphemeralRunner{
				Name:      runnerName,
				Namespace: ers.Namespace,
				Labels: map[string]string{
					runner.LabelManagedBy:    runner.LabelManagedByValue,
					runner.LabelScaleSetName: ers.Spec.ScaleSetRef.Name,
				},
				Spec: ghav1alpha1.EphemeralRunnerSpec{
					ScaleSetRef: corev1.LocalObjectReference{
						Name: ers.Spec.ScaleSetRef.Name,
					},
					RunnerName: runnerName,
				},
				Status: ghav1alpha1.EphemeralRunnerStatus{
					Phase: ghav1alpha1.EphemeralRunnerPhasePending,
				},
			}

			if err := controllerutil.SetControllerReference(&ers, newRunner, r.Scheme); err != nil {
				log.Error(err, "failed to set controller reference on runner", "runner", runnerName)
				continue
			}

			if err := r.Create(ctx, newRunner); err != nil {
				log.Error(err, "failed to create EphemeralRunner", "runner", runnerName)
				continue
			}
			log.Info("created EphemeralRunner", "runner", runnerName)
		}
	}

	// 3. スケールダウン (target < active)
	// アイドルまたは未起動の Runner から優先してスケールダウン
	if targetReplicas < int32(len(activeRunners)) {
		excess := int32(len(activeRunners)) - targetReplicas
		log.Info("scaling down EphemeralRunners", "excess", excess)
		for _, run := range activeRunners {
			if excess <= 0 {
				break
			}
			if run.Status.Phase == ghav1alpha1.EphemeralRunnerPhasePending ||
				run.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster ||
				run.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseIdle {
				if err := r.Delete(ctx, run); err != nil {
					log.Error(err, "failed to delete EphemeralRunner for scale-down", "runner", run.Name)
				} else {
					excess--
				}
			}
		}
	}

	// 4. Conditions 更新
	if ers.Status.ActiveReplicas == targetReplicas {
		conditions.SetConditionWithGeneration(&ers.Status.Conditions, ers.Generation, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonReady, "EphemeralRunnerSet replicas satisfied")
	} else {
		conditions.SetConditionWithGeneration(&ers.Status.Conditions, ers.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonPending, "Replicas adjusting")
	}

	// 5. Status 更新
	if err := r.updateStatus(ctx, &ers, origERS); err != nil {
		log.Error(err, "failed to patch EphemeralRunnerSet status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *EphemeralRunnerSetReconciler) updateStatus(ctx context.Context, ers, _ *ghav1alpha1.EphemeralRunnerSet) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current ghav1alpha1.EphemeralRunnerSet
		if err := r.Get(ctx, client.ObjectKeyFromObject(ers), &current); err != nil {
			return err
		}
		orig := current.DeepCopy()
		current.Status = ers.Status
		patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
		return r.Status().Patch(ctx, &current, patch)
	})
}

func (r *EphemeralRunnerSetReconciler) findRunnerSetsForRunner(ctx context.Context, obj client.Object) []ctrl.Request {
	run, ok := obj.(*ghav1alpha1.EphemeralRunner)
	if !ok || run.Spec.ScaleSetRef.Name == "" {
		return nil
	}

	var ersList ghav1alpha1.EphemeralRunnerSetList
	if err := r.List(ctx, &ersList, client.InNamespace(run.Namespace), client.MatchingFields{
		IndexScaleSetRefName: run.Spec.ScaleSetRef.Name,
	}); err != nil {
		return nil
	}

	var reqs []ctrl.Request
	for _, ers := range ersList.Items {
		reqs = append(reqs, ctrl.Request{
			Namespace: ers.Namespace,
			Name:      ers.Name,
		})
	}
	return reqs
}

// SetupWithManager sets up the controller with the Manager.
func (r *EphemeralRunnerSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ghav1alpha1.EphemeralRunnerSet{}).
		Watches(&ghav1alpha1.EphemeralRunner{}, handler.EnqueueRequestsFromMapFunc(r.findRunnerSetsForRunner)).
		Complete(r)
}
