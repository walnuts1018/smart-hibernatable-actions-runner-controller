package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1apply "k8s.io/client-go/applyconfigurations/apps/v1"
	coordinationv1apply "k8s.io/client-go/applyconfigurations/coordination/v1"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	rbacv1apply "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/conditions"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/githubscaleset"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/runner"
)

// RunnerScaleSetReconciler reconciles a RunnerScaleSet object.
type RunnerScaleSetReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	Recorder        events.EventRecorder
	ScaleSetFactory githubscaleset.ScaleSetClientFactory
	ListenerImage   string
}

// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnerscalesets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnerscalesets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnerscalesets/finalizers,verbs=update
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=ephemeralrunnersets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=ephemeralrunnersets/status,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=ephemeralrunners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=ephemeralrunners/status,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnernodepools,verbs=get;list;watch
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnermachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

func (r *RunnerScaleSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var scaleSet ghav1alpha1.RunnerScaleSet
	if err := r.Get(ctx, req.NamespacedName, &scaleSet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	origScaleSet := scaleSet.DeepCopy()
	scaleSet.Status.ObservedGeneration = scaleSet.Generation

	// 1. Finalizer処理 (削除時: Drain-first)
	if !scaleSet.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, &scaleSet, origScaleSet)
	}

	// Finalizer確保
	if !controllerutil.ContainsFinalizer(&scaleSet, runner.FinalizerScaleSetCleanup) {
		controllerutil.AddFinalizer(&scaleSet, runner.FinalizerScaleSetCleanup)
		if err := r.Update(ctx, &scaleSet); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	hasReconcileError := false

	// 2. GitHub ScaleSet の同期
	if err := r.reconcileGitHub(ctx, &scaleSet); err != nil {
		log.Error(err, "failed to reconcile GitHub ScaleSet")
		hasReconcileError = true
	}

	// 3. CapacityLimit / EffectiveMaxRunners の計算
	if _, err := r.reconcileCapacity(ctx, &scaleSet); err != nil {
		log.Error(err, "failed to reconcile capacity")
		hasReconcileError = true
	}

	// 4. Listener 関連リソースの Server-Side Apply
	if err := r.reconcileListener(ctx, &scaleSet); err != nil {
		log.Error(err, "failed to reconcile listener resources")
		hasReconcileError = true
	}

	// 5. EphemeralRunnerSet リソースの同期
	if err := r.reconcileEphemeralRunnerSet(ctx, &scaleSet); err != nil {
		log.Error(err, "failed to reconcile ephemeral runner set")
		hasReconcileError = true
	}

	// 6. 全体 Ready Condition の更新
	if scaleSet.Status.GitHub.AssignedJobs >= 0 && scaleSet.Status.Listener.Ready && scaleSet.Status.ScaleSetID > 0 {
		conditions.SetConditionWithGeneration(&scaleSet.Status.Conditions, scaleSet.Generation, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonReady, "ScaleSet is operational")
	} else {
		conditions.SetConditionWithGeneration(&scaleSet.Status.Conditions, scaleSet.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "ScaleSet is initializing")
	}

	// 7. Status の更新
	if err := r.updateStatus(ctx, &scaleSet, origScaleSet); err != nil {
		log.Error(err, "failed to update runner scale set status")
		return ctrl.Result{}, err
	}

	metrics.EffectiveMaxRunners.WithLabelValues(scaleSet.Namespace, scaleSet.Name).Set(float64(scaleSet.Status.EffectiveMaxRunners))

	if hasReconcileError {
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *RunnerScaleSetReconciler) reconcileDeletion(
	ctx context.Context,
	scaleSet, origScaleSet *ghav1alpha1.RunnerScaleSet,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(scaleSet, runner.FinalizerScaleSetCleanup) {
		return ctrl.Result{}, nil
	}

	// 1.1 新規生成停止のため effectiveMaxRunners = 0 に設定
	if scaleSet.Status.EffectiveMaxRunners != 0 {
		scaleSet.Status.EffectiveMaxRunners = 0
		if updateErr := r.updateStatus(ctx, scaleSet, origScaleSet); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
	}

	// 1.2 配下の EphemeralRunnerSet の replicas を 0 にスケールダウン
	var ers ghav1alpha1.EphemeralRunnerSet
	if err := r.Get(ctx, client.ObjectKey{Namespace: scaleSet.Namespace, Name: scaleSet.Name}, &ers); err == nil {
		zero := int32(0)
		if ers.Spec.Replicas == nil || *ers.Spec.Replicas != 0 {
			origERS := ers.DeepCopy()
			ers.Spec.Replicas = &zero
			if err := r.Patch(ctx, &ers, client.MergeFrom(origERS)); err != nil {
				log.Error(err, "failed to scale down EphemeralRunnerSet during deletion")
			}
		}
	}

	// 1.3 配下の非完了 EphemeralRunner をカウント
	var runnerList ghav1alpha1.EphemeralRunnerList
	if err := r.List(ctx, &runnerList, client.InNamespace(scaleSet.Namespace), client.MatchingFields{
		IndexScaleSetRefName: scaleSet.Name,
	}); err != nil {
		return ctrl.Result{}, err
	}

	activeCount := 0
	for _, run := range runnerList.Items {
		matches := false
		if run.Labels[runner.LabelScaleSetUID] != "" {
			matches = run.Labels[runner.LabelScaleSetUID] == string(scaleSet.UID)
		} else {
			matches = run.Spec.ScaleSetRef.Name == scaleSet.Name
		}
		if matches && isRunnerNonTerminal(run.Status.Phase) {
			activeCount++
		}
	}

	if activeCount > 0 {
		log.Info("waiting for in-flight runner jobs to complete before deleting RunnerScaleSet", "scaleSet", scaleSet.Name, "activeRunners", activeCount)
		if r.Recorder != nil {
			r.Recorder.Eventf(scaleSet, nil, corev1.EventTypeNormal, "DrainingRunners", "Delete", "Waiting for %d in-flight runner(s) to complete before cleanup", activeCount)
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// 1.4 Listener Deployment の削除
	deployName := fmt.Sprintf("%s-listener", scaleSet.Name)
	var deploy appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKey{Namespace: scaleSet.Namespace, Name: deployName}, &deploy); err == nil {
		if err := r.Delete(ctx, &deploy); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "failed to delete listener deployment")
		}
	}

	// 1.5 GitHub 側 ScaleSet の削除 (ScaleSetID が既知の場合)
	if scaleSet.Status.ScaleSetID > 0 {
		ghaClient, err := r.getGitHubClient(ctx, scaleSet)
		if err == nil {
			if err := ghaClient.DeleteScaleSet(ctx, scaleSet.Status.ScaleSetID); err != nil {
				log.Error(err, "failed to delete RunnerScaleSet in GitHub Actions, proceeding with finalizer removal", "scaleSetID", scaleSet.Status.ScaleSetID)
			} else {
				log.Info("successfully deleted RunnerScaleSet in GitHub Actions", "scaleSetID", scaleSet.Status.ScaleSetID)
			}
		}
	}

	// 1.6 Finalizer の削除
	controllerutil.RemoveFinalizer(scaleSet, runner.FinalizerScaleSetCleanup)
	if err := r.Update(ctx, scaleSet); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("successfully finalized and cleaned up RunnerScaleSet", "scaleSet", scaleSet.Name)
	return ctrl.Result{}, nil
}

func (r *RunnerScaleSetReconciler) updateStatus(ctx context.Context, ss, _ *ghav1alpha1.RunnerScaleSet) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current ghav1alpha1.RunnerScaleSet
		if err := r.Get(ctx, client.ObjectKeyFromObject(ss), &current); err != nil {
			return err
		}
		orig := current.DeepCopy()
		current.Status = ss.Status
		patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
		return r.Status().Patch(ctx, &current, patch)
	})
}

func (r *RunnerScaleSetReconciler) reconcileGitHub(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet) error {
	ghaClient, err := r.getGitHubClient(ctx, ss)
	if err != nil {
		if r.Recorder != nil {
			r.Recorder.Eventf(ss, nil, corev1.EventTypeWarning, "GitHubAuthFailed", "Reconcile", "Failed to authenticate with GitHub App: %v", err)
		}
		conditions.SetConditionWithGeneration(&ss.Status.Conditions, ss.Generation, conditions.TypeGitHubReady, metav1.ConditionFalse, conditions.ReasonGitHubAuthFailed, err.Error())
		conditions.SetConditionWithGeneration(&ss.Status.Conditions, ss.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "GitHub client auth failed")
		return err
	}

	scaleSetID, err := ghaClient.GetOrCreateScaleSet(ctx, ss.Spec.GitHub.ScaleSetName, ss.Spec.GitHub.RunnerGroup)
	if err != nil {
		if r.Recorder != nil {
			r.Recorder.Eventf(ss, nil, corev1.EventTypeWarning, "ScaleSetFailed", "Reconcile", "Failed to ensure scale set in GitHub: %v", err)
		}
		conditions.SetConditionWithGeneration(&ss.Status.Conditions, ss.Generation, conditions.TypeGitHubReady, metav1.ConditionFalse, conditions.ReasonScaleSetFailed, err.Error())
		conditions.SetConditionWithGeneration(&ss.Status.Conditions, ss.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "Failed to create scale set in GitHub")
		return err
	}

	if ss.Status.ScaleSetID != scaleSetID {
		ss.Status.ScaleSetID = scaleSetID
		if r.Recorder != nil {
			r.Recorder.Eventf(ss, nil, corev1.EventTypeNormal, "ScaleSetRegistered", "Reconcile", "Registered RunnerScaleSet in GitHub Actions with ID %d", scaleSetID)
		}
	}
	conditions.SetConditionWithGeneration(&ss.Status.Conditions, ss.Generation, conditions.TypeGitHubReady, metav1.ConditionTrue, conditions.ReasonScaleSetCreated, "ScaleSet is registered in GitHub Actions")
	return nil
}

func (r *RunnerScaleSetReconciler) reconcileCapacity(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet) (int32, error) {
	var nodePool ghav1alpha1.RunnerNodePool
	if err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: ss.Spec.NodePoolRef.Name}, &nodePool); err != nil {
		if r.Recorder != nil {
			r.Recorder.Eventf(ss, nil, corev1.EventTypeWarning, "NodePoolNotFound", "Reconcile", "Referenced NodePool %s not found: %v", ss.Spec.NodePoolRef.Name, err)
		}
		conditions.SetConditionWithGeneration(&ss.Status.Conditions, ss.Generation, conditions.TypeCapacityReady, metav1.ConditionFalse, conditions.ReasonNotReady, fmt.Sprintf("NodePool %s not found", ss.Spec.NodePoolRef.Name))
		return 0, err
	}

	conditions.SetConditionWithGeneration(&ss.Status.Conditions, ss.Generation, conditions.TypeCapacityReady, metav1.ConditionTrue, conditions.ReasonCapacitySufficient, "NodePool found")

	effectiveMax := int32(0)
	if ss.Spec.Scaling.MaxRunners != nil {
		effectiveMax = *ss.Spec.Scaling.MaxRunners
	}
	if ss.Spec.Suspend {
		effectiveMax = 0
	}
	ss.Status.EffectiveMaxRunners = effectiveMax

	return effectiveMax, nil
}

func (r *RunnerScaleSetReconciler) reconcileListener(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet) error {
	owner, err := controllerReference(ss, r.Scheme)
	if err != nil {
		return fmt.Errorf("failed to create owner reference: %w", err)
	}

	labels := map[string]string{
		runner.LabelManagedBy:         runner.LabelManagedByValue,
		runner.LabelScaleSetUID:       string(ss.UID),
		runner.LabelScaleSetName:      ss.Name,
		"app.kubernetes.io/component": "listener",
	}

	if err := r.reconcileListenerServiceAccount(ctx, ss, owner, labels); err != nil {
		r.recordListenerError(ss, err)
		return fmt.Errorf("failed to reconcile listener service account: %w", err)
	}

	if err := r.reconcileListenerRole(ctx, ss, owner, labels); err != nil {
		r.recordListenerError(ss, err)
		return fmt.Errorf("failed to reconcile listener role: %w", err)
	}

	if err := r.reconcileListenerRoleBinding(ctx, ss, owner, labels); err != nil {
		r.recordListenerError(ss, err)
		return fmt.Errorf("failed to reconcile listener role binding: %w", err)
	}

	if err := r.reconcileListenerLease(ctx, ss, owner, labels); err != nil {
		r.recordListenerError(ss, err)
		return fmt.Errorf("failed to reconcile listener lease: %w", err)
	}

	if err := r.reconcileListenerDeployment(ctx, ss, owner, labels); err != nil {
		r.recordListenerError(ss, err)
		return fmt.Errorf("failed to reconcile listener deployment: %w", err)
	}

	conditions.SetConditionWithGeneration(&ss.Status.Conditions, ss.Generation, conditions.TypeListenerReady, metav1.ConditionTrue, conditions.ReasonListenerRunning, "Listener deployment is running")
	ss.Status.Listener.Ready = true
	return nil
}

func (r *RunnerScaleSetReconciler) recordListenerError(ss *ghav1alpha1.RunnerScaleSet, err error) {
	if r.Recorder != nil {
		r.Recorder.Eventf(ss, nil, corev1.EventTypeWarning, "ListenerFailed", "Reconcile", "Failed to ensure listener resources: %v", err)
	}
	conditions.SetConditionWithGeneration(&ss.Status.Conditions, ss.Generation, conditions.TypeListenerReady, metav1.ConditionFalse, conditions.ReasonListenerNotRunning, err.Error())
}

func (r *RunnerScaleSetReconciler) reconcileListenerServiceAccount(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet, owner *metav1apply.OwnerReferenceApplyConfiguration, labels map[string]string) error {
	saName := fmt.Sprintf("%s-listener", ss.Name)
	desiredSA := corev1apply.ServiceAccount(saName, ss.Namespace).
		WithLabels(labels).
		WithOwnerReferences(owner)

	var current corev1.ServiceAccount
	err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: saName}, &current)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	currentApply, err := corev1apply.ExtractServiceAccount(&current, FieldManagerName)
	if err == nil && equality.Semantic.DeepEqual(desiredSA, currentApply) {
		return nil
	}

	return applyResource(ctx, r.Client, desiredSA)
}

func (r *RunnerScaleSetReconciler) reconcileListenerRole(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet, owner *metav1apply.OwnerReferenceApplyConfiguration, labels map[string]string) error {
	roleName := fmt.Sprintf("%s-listener", ss.Name)
	leaseName := fmt.Sprintf("gha-listener-%s", ss.UID)

	rules := []*rbacv1apply.PolicyRuleApplyConfiguration{
		rbacv1apply.PolicyRule().
			WithAPIGroups("sharc.walnuts.dev").
			WithResources("runnerscalesets").
			WithResourceNames(ss.Name).
			WithVerbs("get"),
		rbacv1apply.PolicyRule().
			WithAPIGroups("sharc.walnuts.dev").
			WithResources("runnerscalesets/status").
			WithResourceNames(ss.Name).
			WithVerbs("get", "patch"),
		rbacv1apply.PolicyRule().
			WithAPIGroups("sharc.walnuts.dev").
			WithResources("ephemeralrunnersets", "ephemeralrunnersets/status").
			WithVerbs("get", "list", "watch", "update", "patch"),
		rbacv1apply.PolicyRule().
			WithAPIGroups("sharc.walnuts.dev").
			WithResources("ephemeralrunners", "ephemeralrunners/status").
			WithVerbs("get", "list", "watch", "update", "patch"),
		rbacv1apply.PolicyRule().
			WithAPIGroups("sharc.walnuts.dev").
			WithResources("runnernodepools", "runnermachines").
			WithVerbs("get", "list"),
		rbacv1apply.PolicyRule().
			WithAPIGroups("").
			WithResources("secrets").
			WithResourceNames(ss.Spec.GitHub.CredentialsSecretRef.Name).
			WithVerbs("get"),
		rbacv1apply.PolicyRule().
			WithAPIGroups("coordination.k8s.io").
			WithResources("leases").
			WithResourceNames(leaseName).
			WithVerbs("get", "update", "patch"),
	}

	desiredRole := rbacv1apply.Role(roleName, ss.Namespace).
		WithLabels(labels).
		WithOwnerReferences(owner).
		WithRules(rules...)

	var current rbacv1.Role
	err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: roleName}, &current)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	currentApply, err := rbacv1apply.ExtractRole(&current, FieldManagerName)
	if err == nil && equality.Semantic.DeepEqual(desiredRole, currentApply) {
		return nil
	}

	return applyResource(ctx, r.Client, desiredRole)
}

func (r *RunnerScaleSetReconciler) reconcileListenerRoleBinding(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet, owner *metav1apply.OwnerReferenceApplyConfiguration, labels map[string]string) error {
	rbName := fmt.Sprintf("%s-listener", ss.Name)
	saName := fmt.Sprintf("%s-listener", ss.Name)
	roleName := fmt.Sprintf("%s-listener", ss.Name)

	desiredRB := rbacv1apply.RoleBinding(rbName, ss.Namespace).
		WithLabels(labels).
		WithOwnerReferences(owner).
		WithRoleRef(rbacv1apply.RoleRef().
			WithAPIGroup("rbac.authorization.k8s.io").
			WithKind("Role").
			WithName(roleName),
		).
		WithSubjects(rbacv1apply.Subject().
			WithKind("ServiceAccount").
			WithName(saName).
			WithNamespace(ss.Namespace),
		)

	var current rbacv1.RoleBinding
	err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: rbName}, &current)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	currentApply, err := rbacv1apply.ExtractRoleBinding(&current, FieldManagerName)
	if err == nil && equality.Semantic.DeepEqual(desiredRB, currentApply) {
		return nil
	}

	return applyResource(ctx, r.Client, desiredRB)
}

func (r *RunnerScaleSetReconciler) reconcileListenerLease(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet, owner *metav1apply.OwnerReferenceApplyConfiguration, labels map[string]string) error {
	leaseName := fmt.Sprintf("gha-listener-%s", ss.UID)
	desiredLease := coordinationv1apply.Lease(leaseName, ss.Namespace).
		WithLabels(labels).
		WithOwnerReferences(owner)

	return applyResource(ctx, r.Client, desiredLease)
}

func (r *RunnerScaleSetReconciler) buildListenerContainer(ss *ghav1alpha1.RunnerScaleSet, listenerImage string) *corev1apply.ContainerApplyConfiguration {
	containerApply := corev1apply.Container().
		WithName("listener").
		WithImage(listenerImage).
		WithCommand(
			"/listener",
			fmt.Sprintf("--runner-scale-set=%s/%s", ss.Namespace, ss.Name),
			"--health-probe-bind-address=:8081",
		).
		WithSecurityContext(corev1apply.SecurityContext().
			WithAllowPrivilegeEscalation(false).
			WithReadOnlyRootFilesystem(true).
			WithCapabilities(corev1apply.Capabilities().
				WithDrop("ALL"),
			),
		).
		WithPorts(
			corev1apply.ContainerPort().
				WithName("health").
				WithContainerPort(8081).
				WithProtocol(corev1.ProtocolTCP),
		).
		WithEnv(
			corev1apply.EnvVar().
				WithName("POD_NAME").
				WithValueFrom(corev1apply.EnvVarSource().
					WithFieldRef(corev1apply.ObjectFieldSelector().
						WithFieldPath("metadata.name"),
					),
				),
			corev1apply.EnvVar().
				WithName("POD_NAMESPACE").
				WithValueFrom(corev1apply.EnvVarSource().
					WithFieldRef(corev1apply.ObjectFieldSelector().
						WithFieldPath("metadata.namespace"),
					),
				),
			corev1apply.EnvVar().
				WithName("HOME").
				WithValue("/tmp"),
		).
		WithVolumeMounts(
			corev1apply.VolumeMount().
				WithName("tmp").
				WithMountPath("/tmp"),
		).
		WithLivenessProbe(corev1apply.Probe().
			WithHTTPGet(corev1apply.HTTPGetAction().
				WithPath("/healthz").
				WithPort(intstr.FromInt(8081)),
			).
			WithInitialDelaySeconds(10).
			WithPeriodSeconds(15),
		).
		WithReadinessProbe(corev1apply.Probe().
			WithHTTPGet(corev1apply.HTTPGetAction().
				WithPath("/readyz").
				WithPort(intstr.FromInt(8081)),
			).
			WithInitialDelaySeconds(2).
			WithPeriodSeconds(5),
		)

	if ss.Spec.Listener.ImagePullPolicy != "" {
		containerApply = containerApply.WithImagePullPolicy(ss.Spec.Listener.ImagePullPolicy)
	}

	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if found && strings.HasPrefix(key, "OTEL_") {
			containerApply = containerApply.WithEnv(corev1apply.EnvVar().WithName(key).WithValue(value))
		}
	}

	for _, ev := range ss.Spec.Listener.Env {
		var envApply corev1apply.EnvVarApplyConfiguration
		if raw, err := json.Marshal(ev); err == nil {
			if err := json.Unmarshal(raw, &envApply); err == nil {
				containerApply = containerApply.WithEnv(&envApply)
			}
		}
	}

	if ss.Spec.Listener.ContainerSecurityContext != nil {
		var cSecApply corev1apply.SecurityContextApplyConfiguration
		if raw, err := json.Marshal(ss.Spec.Listener.ContainerSecurityContext); err == nil {
			if err := json.Unmarshal(raw, &cSecApply); err == nil {
				containerApply = containerApply.WithSecurityContext(&cSecApply)
			}
		}
	}

	if ss.Spec.Listener.Resources.Limits != nil || ss.Spec.Listener.Resources.Requests != nil {
		resApply := corev1apply.ResourceRequirements()
		if ss.Spec.Listener.Resources.Limits != nil {
			resApply = resApply.WithLimits(ss.Spec.Listener.Resources.Limits)
		}
		if ss.Spec.Listener.Resources.Requests != nil {
			resApply = resApply.WithRequests(ss.Spec.Listener.Resources.Requests)
		}
		containerApply = containerApply.WithResources(resApply)
	}

	return containerApply
}

func (r *RunnerScaleSetReconciler) buildListenerPodSpec(ss *ghav1alpha1.RunnerScaleSet, saName string, containerApply *corev1apply.ContainerApplyConfiguration) *corev1apply.PodSpecApplyConfiguration {
	podSpecApply := corev1apply.PodSpec().
		WithServiceAccountName(saName).
		WithContainers(containerApply).
		WithVolumes(
			corev1apply.Volume().
				WithName("tmp").
				WithEmptyDir(corev1apply.EmptyDirVolumeSource()),
		).
		WithRestartPolicy(corev1.RestartPolicyAlways)

	if len(ss.Spec.Listener.NodeSelector) > 0 {
		podSpecApply = podSpecApply.WithNodeSelector(ss.Spec.Listener.NodeSelector)
	}

	if len(ss.Spec.Listener.Tolerations) > 0 {
		for _, t := range ss.Spec.Listener.Tolerations {
			var tolApply corev1apply.TolerationApplyConfiguration
			if raw, err := json.Marshal(t); err == nil {
				if err := json.Unmarshal(raw, &tolApply); err == nil {
					podSpecApply = podSpecApply.WithTolerations(&tolApply)
				}
			}
		}
	}

	if ss.Spec.Listener.Affinity != nil {
		var affApply corev1apply.AffinityApplyConfiguration
		if raw, err := json.Marshal(ss.Spec.Listener.Affinity); err == nil {
			if err := json.Unmarshal(raw, &affApply); err == nil {
				podSpecApply = podSpecApply.WithAffinity(&affApply)
			}
		}
	}

	if len(ss.Spec.Listener.TopologySpreadConstraints) > 0 {
		for _, tsc := range ss.Spec.Listener.TopologySpreadConstraints {
			var tscApply corev1apply.TopologySpreadConstraintApplyConfiguration
			if raw, err := json.Marshal(tsc); err == nil {
				if err := json.Unmarshal(raw, &tscApply); err == nil {
					podSpecApply = podSpecApply.WithTopologySpreadConstraints(&tscApply)
				}
			}
		}
	}

	if ss.Spec.Listener.PriorityClassName != "" {
		podSpecApply = podSpecApply.WithPriorityClassName(ss.Spec.Listener.PriorityClassName)
	}

	if len(ss.Spec.Listener.ImagePullSecrets) > 0 {
		for _, ips := range ss.Spec.Listener.ImagePullSecrets {
			var ipsApply corev1apply.LocalObjectReferenceApplyConfiguration
			if raw, err := json.Marshal(ips); err == nil {
				if err := json.Unmarshal(raw, &ipsApply); err == nil {
					podSpecApply = podSpecApply.WithImagePullSecrets(&ipsApply)
				}
			}
		}
	}

	if ss.Spec.Listener.SecurityContext != nil {
		var pSecApply corev1apply.PodSecurityContextApplyConfiguration
		if raw, err := json.Marshal(ss.Spec.Listener.SecurityContext); err == nil {
			if err := json.Unmarshal(raw, &pSecApply); err == nil {
				podSpecApply = podSpecApply.WithSecurityContext(&pSecApply)
			}
		}
	} else {
		podSpecApply = podSpecApply.WithSecurityContext(corev1apply.PodSecurityContext().
			WithRunAsNonRoot(true).
			WithRunAsUser(65532).
			WithRunAsGroup(65532).
			WithFSGroup(65532).
			WithSeccompProfile(corev1apply.SeccompProfile().
				WithType(corev1.SeccompProfileTypeRuntimeDefault),
			),
		)
	}

	return podSpecApply
}

func (r *RunnerScaleSetReconciler) reconcileListenerDeployment(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet, owner *metav1apply.OwnerReferenceApplyConfiguration, labels map[string]string) error {
	deployName := fmt.Sprintf("%s-listener", ss.Name)
	saName := fmt.Sprintf("%s-listener", ss.Name)
	if ss.Spec.Listener.ServiceAccountName != "" {
		saName = ss.Spec.Listener.ServiceAccountName
	}

	listenerImage := r.ListenerImage
	if ss.Spec.Listener.Image != "" {
		listenerImage = ss.Spec.Listener.Image
	} else if listenerImage == "" {
		listenerImage = runner.DefaultListenerImage
	}

	credHash := r.computeSecretHash(ctx, ss.Namespace, ss.Spec.GitHub.CredentialsSecretRef.Name)

	maxSurge := intstr.FromInt(0)
	maxUnavailable := intstr.FromInt(1)

	containerApply := r.buildListenerContainer(ss, listenerImage)

	podLabels := make(map[string]string)
	maps.Copy(podLabels, ss.Spec.Listener.Labels)
	maps.Copy(podLabels, labels)

	podAnnotations := make(map[string]string)
	maps.Copy(podAnnotations, ss.Spec.Listener.Annotations)
	podAnnotations[runner.AnnotationCredentialsHash] = credHash

	podSpecApply := r.buildListenerPodSpec(ss, saName, containerApply)

	desiredDeploy := appsv1apply.Deployment(deployName, ss.Namespace).
		WithLabels(labels).
		WithOwnerReferences(owner).
		WithSpec(appsv1apply.DeploymentSpec().
			WithReplicas(1).
			WithStrategy(appsv1apply.DeploymentStrategy().
				WithType(appsv1.RollingUpdateDeploymentStrategyType).
				WithRollingUpdate(appsv1apply.RollingUpdateDeployment().
					WithMaxSurge(maxSurge).
					WithMaxUnavailable(maxUnavailable),
				),
			).
			WithSelector(metav1apply.LabelSelector().WithMatchLabels(labels)).
			WithTemplate(corev1apply.PodTemplateSpec().
				WithLabels(podLabels).
				WithAnnotations(podAnnotations).
				WithSpec(podSpecApply),
			),
		)

	var current appsv1.Deployment
	err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: deployName}, &current)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	currentApply, err := appsv1apply.ExtractDeployment(&current, FieldManagerName)
	if err == nil && equality.Semantic.DeepEqual(desiredDeploy, currentApply) {
		return nil
	}

	return applyResource(ctx, r.Client, desiredDeploy)
}

func (r *RunnerScaleSetReconciler) reconcileEphemeralRunnerSet(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet) error {
	log := logf.FromContext(ctx)

	// ScaleSet に紐づく EphemeralRunnerSet を取得または作成
	var ers ghav1alpha1.EphemeralRunnerSet
	ersName := ss.Name
	err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: ersName}, &ers)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if apierrors.IsNotFound(err) {
		log.Info("creating EphemeralRunnerSet for RunnerScaleSet", "scaleSet", ss.Name)
		zero := int32(0)
		ers = ghav1alpha1.EphemeralRunnerSet{
			Name:      ersName,
			Namespace: ss.Namespace,
			Labels: map[string]string{
				runner.LabelManagedBy:    runner.LabelManagedByValue,
				runner.LabelScaleSetUID:  string(ss.UID),
				runner.LabelScaleSetName: ss.Name,
			},
			Spec: ghav1alpha1.EphemeralRunnerSetSpec{
				ScaleSetRef: corev1.LocalObjectReference{
					Name: ss.Name,
				},
				Replicas: &zero,
				Runner:   ss.Spec.Runner,
			},
		}
		if err := controllerutil.SetControllerReference(ss, &ers, r.Scheme); err != nil {
			return err
		}
		if err := r.Create(ctx, &ers); err != nil {
			return err
		}
	} else {
		// テンプレートとScaleSetRefの同期
		origERS := ers.DeepCopy()
		ers.Spec.ScaleSetRef.Name = ss.Name
		ers.Spec.Runner = ss.Spec.Runner
		if !equality.Semantic.DeepEqual(origERS.Spec, ers.Spec) {
			if err := r.Update(ctx, &ers); err != nil {
				return err
			}
		}
	}

	// ActiveRunners をカウント
	var runnerList ghav1alpha1.EphemeralRunnerList
	if err := r.List(ctx, &runnerList, client.InNamespace(ss.Namespace), client.MatchingFields{
		IndexScaleSetRefName: ss.Name,
	}); err == nil {
		activeCount := int32(0)
		for _, run := range runnerList.Items {
			if isRunnerNonTerminal(run.Status.Phase) {
				activeCount++
			}
		}
		ss.Status.ActiveRunners = activeCount
	}

	return nil
}

func (r *RunnerScaleSetReconciler) computeSecretHash(ctx context.Context, namespace, secretName string) string {
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, &secret); err != nil {
		return ""
	}

	h := sha256.New()
	for _, k := range slices.Sorted(maps.Keys(secret.Data)) {
		h.Write([]byte(k))
		h.Write(secret.Data[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (r *RunnerScaleSetReconciler) getGitHubClient(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet) (githubscaleset.ScaleSetClient, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: ss.Spec.GitHub.CredentialsSecretRef.Name}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get credentials secret %s: %w", ss.Spec.GitHub.CredentialsSecretRef.Name, err)
	}

	auth, err := githubscaleset.ParseGitHubAppAuth(secret.Data)
	if err != nil {
		return nil, err
	}

	return r.ScaleSetFactory.NewClient(ss.Spec.GitHub.ConfigURL, auth)
}

func (r *RunnerScaleSetReconciler) findScaleSetsForNodePool(ctx context.Context, obj client.Object) []ctrl.Request {
	pool, ok := obj.(*ghav1alpha1.RunnerNodePool)
	if !ok {
		return nil
	}

	var scaleSets ghav1alpha1.RunnerScaleSetList
	if err := listWithIndexFallback(ctx, r.Client, &scaleSets, pool.Namespace, IndexNodePoolRefName, pool.Name); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, ss := range scaleSets.Items {
		if ss.Spec.NodePoolRef.Name == pool.Name {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&ss),
			})
		}
	}
	return requests
}

func (r *RunnerScaleSetReconciler) findScaleSetsForRunner(ctx context.Context, obj client.Object) []ctrl.Request {
	runner, ok := obj.(*ghav1alpha1.EphemeralRunner)
	if !ok || runner.Spec.ScaleSetRef.Name == "" {
		return nil
	}

	return []ctrl.Request{
		{
			Namespace: runner.Namespace,
			Name:      runner.Spec.ScaleSetRef.Name,
		},
	}
}

func (r *RunnerScaleSetReconciler) findScaleSetsForRunnerSet(ctx context.Context, obj client.Object) []ctrl.Request {
	ers, ok := obj.(*ghav1alpha1.EphemeralRunnerSet)
	if !ok || ers.Spec.ScaleSetRef.Name == "" {
		return nil
	}

	return []ctrl.Request{
		{
			Namespace: ers.Namespace,
			Name:      ers.Spec.ScaleSetRef.Name,
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *RunnerScaleSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ghav1alpha1.RunnerScaleSet{}).
		Watches(&ghav1alpha1.RunnerNodePool{}, handler.EnqueueRequestsFromMapFunc(r.findScaleSetsForNodePool)).
		Watches(&ghav1alpha1.EphemeralRunnerSet{}, handler.EnqueueRequestsFromMapFunc(r.findScaleSetsForRunnerSet)).
		Watches(&ghav1alpha1.EphemeralRunner{}, handler.EnqueueRequestsFromMapFunc(r.findScaleSetsForRunner)).
		Complete(r)
}
