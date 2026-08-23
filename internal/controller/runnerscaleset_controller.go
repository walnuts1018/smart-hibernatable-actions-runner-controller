package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
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
	"k8s.io/client-go/tools/record"
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
	Recorder        record.EventRecorder
	ScaleSetFactory githubscaleset.ScaleSetClientFactory
	ListenerImage   string
}

// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnerscalesets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnerscalesets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnerscalesets/finalizers,verbs=update
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=ephemeralrunners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnernodepools,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *RunnerScaleSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var scaleSet ghav1alpha1.RunnerScaleSet
	if err := r.Get(ctx, req.NamespacedName, &scaleSet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	origScaleSet := scaleSet.DeepCopy()

	// 1. Finalizer処理 (削除時: Drain-first)
	if !scaleSet.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&scaleSet, runner.FinalizerScaleSetCleanup) {
			// 1.1 新規生成停止のため effectiveMaxRunners = 0 に設定
			if scaleSet.Status.EffectiveMaxRunners != 0 {
				scaleSet.Status.EffectiveMaxRunners = 0
				_ = r.updateStatus(ctx, &scaleSet, origScaleSet)
			}

			// 1.2 子 EphemeralRunner の一覧取得
			var runnerList ghav1alpha1.EphemeralRunnerList
			if err := r.List(ctx, &runnerList, client.InNamespace(scaleSet.Namespace), client.MatchingLabels{
				runner.LabelScaleSetUID: string(scaleSet.UID),
			}); err != nil {
				return ctrl.Result{}, err
			}

			// 1.3 non-terminal な EphemeralRunner の残存確認 (Drain)
			nonTerminalCount := 0
			for i := range runnerList.Items {
				if isRunnerNonTerminal(runnerList.Items[i].Status.Phase) {
					nonTerminalCount++
				}
			}

			if nonTerminalCount > 0 {
				log.Info("waiting for non-terminal child ephemeral runners to drain before removing scale set", "nonTerminalCount", nonTerminalCount)
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}

			// 1.4 non-terminal が 0 になったら、TTL 保持中の terminal CR を即座に削除 (Cascade Cleanup)
			for i := range runnerList.Items {
				er := &runnerList.Items[i]
				if !isRunnerNonTerminal(er.Status.Phase) {
					if err := r.Delete(ctx, er); err != nil && !apierrors.IsNotFound(err) {
						log.Error(err, "failed to delete terminal child ephemeral runner", "runner", er.Name)
						return ctrl.Result{}, err
					}
				}
			}

			if len(runnerList.Items) > 0 {
				log.Info("waiting for child ephemeral runner CRs to be deleted", "count", len(runnerList.Items))
				return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
			}

			// 1.5 Listener Deployment の停止/削除 (active session を閉じて 409 を防止)
			var deploy appsv1.Deployment
			deployKey := client.ObjectKey{Namespace: scaleSet.Namespace, Name: fmt.Sprintf("%s-listener", scaleSet.Name)}
			if err := r.Get(ctx, deployKey, &deploy); err == nil {
				if err := r.Delete(ctx, &deploy); err != nil && !apierrors.IsNotFound(err) {
					log.Error(err, "failed to delete listener deployment before scaleset cleanup")
					return ctrl.Result{}, err
				}
			}

			// 1.6 GitHub Actions ScaleSet 削除
			orphanOverride := scaleSet.Annotations != nil && scaleSet.Annotations[runner.AnnotationOrphanGitHubResource] == "true"
			if scaleSet.Status.ScaleSetID != 0 && !orphanOverride {
				log.Info("deleting runner scale set from GitHub Actions", "scaleSetID", scaleSet.Status.ScaleSetID)
				ghaClient, err := r.getGitHubClient(ctx, &scaleSet)
				if err != nil {
					log.Error(err, "failed to get github client during scale set deletion; retention required unless orphan-github-resource override is set")
					conditions.SetCondition(&scaleSet.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonGitHubAuthFailed, "Cannot delete GitHub ScaleSet: credentials missing")
					_ = r.updateStatus(ctx, &scaleSet, origScaleSet)
					return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
				}

				if err := ghaClient.DeleteScaleSet(ctx, scaleSet.Status.ScaleSetID); err != nil {
					log.Error(err, "failed to delete scale set in GitHub", "scaleSetID", scaleSet.Status.ScaleSetID)
					conditions.SetCondition(&scaleSet.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonScaleSetFailed, fmt.Sprintf("Failed to delete scale set in GitHub: %v", err))
					_ = r.updateStatus(ctx, &scaleSet, origScaleSet)
					return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
				}
			}

			controllerutil.RemoveFinalizer(&scaleSet, runner.FinalizerScaleSetCleanup)
			if err := r.Update(ctx, &scaleSet); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Finalizer確保
	if !controllerutil.ContainsFinalizer(&scaleSet, runner.FinalizerScaleSetCleanup) {
		controllerutil.AddFinalizer(&scaleSet, runner.FinalizerScaleSetCleanup)
		if err := r.Update(ctx, &scaleSet); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 2. GitHub ScaleSet の同期
	if err := r.reconcileGitHub(ctx, &scaleSet); err != nil {
		log.Error(err, "failed to reconcile GitHub ScaleSet")
		_ = r.updateStatus(ctx, &scaleSet, origScaleSet)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// 3. CapacityLimit の判定
	declaredCapacity, err := r.reconcileCapacity(ctx, &scaleSet)
	if err != nil {
		log.Error(err, "failed to reconcile capacity")
		_ = r.updateStatus(ctx, &scaleSet, origScaleSet)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// 4. Listener 関連リソースの Server-Side Apply
	if err := r.reconcileListener(ctx, &scaleSet); err != nil {
		log.Error(err, "failed to reconcile listener resources")
		_ = r.updateStatus(ctx, &scaleSet, origScaleSet)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// 5. EphemeralRunner リソースの Reconciliation
	if err := r.reconcileRunners(ctx, &scaleSet, declaredCapacity); err != nil {
		log.Error(err, "failed to reconcile ephemeral runners")
		_ = r.updateStatus(ctx, &scaleSet, origScaleSet)
		return ctrl.Result{}, err
	}

	// 6. 全体 Ready Condition の更新
	if scaleSet.Status.GitHub.AssignedJobs >= 0 && scaleSet.Status.Listener.Ready {
		conditions.SetCondition(&scaleSet.Status.Conditions, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonReady, "ScaleSet is operational")
	} else {
		conditions.SetCondition(&scaleSet.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "ScaleSet is initializing")
	}

	// 7. Status の更新
	if err := r.updateStatus(ctx, &scaleSet, origScaleSet); err != nil {
		log.Error(err, "failed to update runner scale set status")
		return ctrl.Result{}, err
	}

	metrics.EffectiveMaxRunners.WithLabelValues(scaleSet.Namespace, scaleSet.Name).Set(float64(scaleSet.Status.EffectiveMaxRunners))

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *RunnerScaleSetReconciler) updateStatus(ctx context.Context, ss, orig *ghav1alpha1.RunnerScaleSet) error {
	return r.Status().Patch(ctx, ss, client.MergeFrom(orig))
}

func (r *RunnerScaleSetReconciler) reconcileGitHub(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet) error {
	ghaClient, err := r.getGitHubClient(ctx, ss)
	if err != nil {
		if r.Recorder != nil {
			r.Recorder.Eventf(ss, corev1.EventTypeWarning, "GitHubAuthFailed", "Failed to authenticate with GitHub App: %v", err)
		}
		conditions.SetCondition(&ss.Status.Conditions, conditions.TypeGitHubReady, metav1.ConditionFalse, conditions.ReasonGitHubAuthFailed, err.Error())
		conditions.SetCondition(&ss.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "GitHub client auth failed")
		return err
	}

	scaleSetID, err := ghaClient.GetOrCreateScaleSet(ctx, ss.Spec.GitHub.ScaleSetName, ss.Spec.GitHub.RunnerGroup)
	if err != nil {
		if r.Recorder != nil {
			r.Recorder.Eventf(ss, corev1.EventTypeWarning, "ScaleSetFailed", "Failed to ensure scale set in GitHub: %v", err)
		}
		conditions.SetCondition(&ss.Status.Conditions, conditions.TypeGitHubReady, metav1.ConditionFalse, conditions.ReasonScaleSetFailed, err.Error())
		conditions.SetCondition(&ss.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "Failed to create scale set in GitHub")
		return err
	}

	if ss.Status.ScaleSetID != scaleSetID {
		ss.Status.ScaleSetID = scaleSetID
		if r.Recorder != nil {
			r.Recorder.Eventf(ss, corev1.EventTypeNormal, "ScaleSetRegistered", "Registered RunnerScaleSet in GitHub Actions with ID %d", scaleSetID)
		}
	}
	conditions.SetCondition(&ss.Status.Conditions, conditions.TypeGitHubReady, metav1.ConditionTrue, conditions.ReasonScaleSetCreated, "ScaleSet is registered in GitHub Actions")
	return nil
}

func (r *RunnerScaleSetReconciler) reconcileCapacity(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet) (int32, error) {
	var nodePool ghav1alpha1.RunnerNodePool
	if err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: ss.Spec.NodePoolRef.Name}, &nodePool); err != nil {
		if r.Recorder != nil {
			r.Recorder.Eventf(ss, corev1.EventTypeWarning, "NodePoolNotFound", "Referenced NodePool %s not found: %v", ss.Spec.NodePoolRef.Name, err)
		}
		conditions.SetCondition(&ss.Status.Conditions, conditions.TypeCapacityReady, metav1.ConditionFalse, conditions.ReasonNotReady, fmt.Sprintf("NodePool %s not found", ss.Spec.NodePoolRef.Name))
		return 0, err
	}

	conditions.SetCondition(&ss.Status.Conditions, conditions.TypeCapacityReady, metav1.ConditionTrue, conditions.ReasonCapacitySufficient, "NodePool found")
	declaredCapacity := r.getNodePoolDeclaredCapacity(ctx, &nodePool)

	if ss.Spec.Scaling.MaxRunners > declaredCapacity {
		if r.Recorder != nil {
			r.Recorder.Eventf(ss, corev1.EventTypeWarning, "CapacityExceeded", "MaxRunners (%d) exceeds NodePool declared capacity (%d)", ss.Spec.Scaling.MaxRunners, declaredCapacity)
		}
		conditions.SetCondition(&ss.Status.Conditions, conditions.TypeCapacityLimited, metav1.ConditionTrue, conditions.ReasonCapacityExceeded, "MaxRunners exceeds NodePool declared capacity")
	} else {
		conditions.SetCondition(&ss.Status.Conditions, conditions.TypeCapacityLimited, metav1.ConditionFalse, conditions.ReasonCapacitySufficient, "Capacity within limits")
	}

	return declaredCapacity, nil
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

	conditions.SetCondition(&ss.Status.Conditions, conditions.TypeListenerReady, metav1.ConditionTrue, conditions.ReasonListenerRunning, "Listener deployment is running")
	ss.Status.Listener.Ready = true
	return nil
}

func (r *RunnerScaleSetReconciler) recordListenerError(ss *ghav1alpha1.RunnerScaleSet, err error) {
	if r.Recorder != nil {
		r.Recorder.Eventf(ss, corev1.EventTypeWarning, "ListenerFailed", "Failed to ensure listener resources: %v", err)
	}
	conditions.SetCondition(&ss.Status.Conditions, conditions.TypeListenerReady, metav1.ConditionFalse, conditions.ReasonListenerNotRunning, err.Error())
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
			WithAPIGroups("gha.walnuts.dev").
			WithResources("runnerscalesets").
			WithResourceNames(ss.Name).
			WithVerbs("get"),
		rbacv1apply.PolicyRule().
			WithAPIGroups("gha.walnuts.dev").
			WithResources("runnerscalesets/status").
			WithResourceNames(ss.Name).
			WithVerbs("get", "patch"),
		rbacv1apply.PolicyRule().
			WithAPIGroups("gha.walnuts.dev").
			WithResources("ephemeralrunners", "ephemeralrunners/status").
			WithVerbs("get", "patch"),
		rbacv1apply.PolicyRule().
			WithAPIGroups("gha.walnuts.dev").
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

	var current coordinationv1.Lease
	err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: leaseName}, &current)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	currentApply, err := coordinationv1apply.ExtractLease(&current, FieldManagerName)
	if err == nil && equality.Semantic.DeepEqual(desiredLease, currentApply) {
		return nil
	}

	return applyResource(ctx, r.Client, desiredLease)
}

func (r *RunnerScaleSetReconciler) reconcileListenerDeployment(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet, owner *metav1apply.OwnerReferenceApplyConfiguration, labels map[string]string) error {
	deployName := fmt.Sprintf("%s-listener", ss.Name)
	saName := fmt.Sprintf("%s-listener", ss.Name)
	listenerImage := r.ListenerImage
	if listenerImage == "" {
		listenerImage = "ghcr.io/walnuts1018/smart-hibernatable-actions-runner-controller/listener:latest"
	}

	var credSecret corev1.Secret
	credHash := ""
	if err := r.Get(ctx, client.ObjectKey{Namespace: ss.Namespace, Name: ss.Spec.GitHub.CredentialsSecretRef.Name}, &credSecret); err == nil {
		credHash = fmt.Sprintf("%s-%s", credSecret.UID, credSecret.ResourceVersion)
	}

	maxSurge := intstr.FromInt(0)
	maxUnavailable := intstr.FromInt(1)

	containerApply := corev1apply.Container().
		WithName("listener").
		WithImage(listenerImage).
		WithCommand(
			"/listener",
			fmt.Sprintf("--runner-scale-set=%s/%s", ss.Namespace, ss.Name),
			"--health-probe-bind-address=:8081",
			"--metrics-bind-address=:8080",
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
				WithName("metrics").
				WithContainerPort(8080).
				WithProtocol(corev1.ProtocolTCP),
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
				WithLabels(labels).
				WithAnnotations(map[string]string{
					runner.AnnotationCredentialsHash: credHash,
				}).
				WithSpec(corev1apply.PodSpec().
					WithServiceAccountName(saName).
					WithSecurityContext(corev1apply.PodSecurityContext().
						WithRunAsNonRoot(true).
						WithRunAsUser(65532).
						WithRunAsGroup(65532).
						WithFSGroup(65532).
						WithSeccompProfile(corev1apply.SeccompProfile().
							WithType(corev1.SeccompProfileTypeRuntimeDefault),
						),
					).
					WithContainers(containerApply).
					WithVolumes(
						corev1apply.Volume().
							WithName("tmp").
							WithEmptyDir(corev1apply.EmptyDirVolumeSource()),
					).
					WithRestartPolicy(corev1.RestartPolicyAlways),
				),
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

func (r *RunnerScaleSetReconciler) reconcileRunners(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet, declaredCapacity int32) error {
	log := logf.FromContext(ctx)

	var runnerList ghav1alpha1.EphemeralRunnerList
	if err := r.List(ctx, &runnerList, client.InNamespace(ss.Namespace)); err != nil {
		return err
	}

	var activeRunners []*ghav1alpha1.EphemeralRunner
	for i := range runnerList.Items {
		run := &runnerList.Items[i]
		matches := false
		if run.Labels[runner.LabelScaleSetUID] != "" {
			matches = run.Labels[runner.LabelScaleSetUID] == string(ss.UID)
		} else {
			matches = run.Spec.ScaleSetRef.Name == ss.Name
		}
		if matches && isRunnerNonTerminal(run.Status.Phase) {
			activeRunners = append(activeRunners, run)
		}
	}

	ss.Status.ActiveRunners = int32(len(activeRunners))

	effectiveMax := ss.Spec.Scaling.MaxRunners
	if declaredCapacity < effectiveMax {
		effectiveMax = declaredCapacity
	}
	if ss.Spec.Suspend {
		effectiveMax = 0
	}
	ss.Status.EffectiveMaxRunners = effectiveMax

	targetRunners := ss.Status.DesiredRunners
	if targetRunners > effectiveMax {
		targetRunners = effectiveMax
	}
	if ss.Spec.Suspend {
		targetRunners = 0
	}

	// スケールアップ (target > active)
	if targetRunners > int32(len(activeRunners)) {
		diff := targetRunners - int32(len(activeRunners))
		log.Info("scaling up ephemeral runners", "count", diff)
		if r.Recorder != nil {
			r.Recorder.Eventf(ss, corev1.EventTypeNormal, "ScalingUp", "Scaling up %d ephemeral runner(s) (target: %d, active: %d)", diff, targetRunners, len(activeRunners))
		}
		for i := 0; i < int(diff); i++ {
			runnerName := runner.GenerateRunnerName(ss.Name)
			newRunner := &ghav1alpha1.EphemeralRunner{
				ObjectMeta: metav1.ObjectMeta{
					Name:      runnerName,
					Namespace: ss.Namespace,
					Labels: map[string]string{
						runner.LabelManagedBy:    runner.LabelManagedByValue,
						runner.LabelScaleSetUID:  string(ss.UID),
						runner.LabelScaleSetName: ss.Name,
					},
				},
				Spec: ghav1alpha1.EphemeralRunnerSpec{
					ScaleSetRef: corev1.LocalObjectReference{
						Name: ss.Name,
					},
					RunnerName: runnerName,
				},
				Status: ghav1alpha1.EphemeralRunnerStatus{
					Phase: ghav1alpha1.EphemeralRunnerPhasePending,
				},
			}

			if err := controllerutil.SetControllerReference(ss, newRunner, r.Scheme); err != nil {
				log.Error(err, "failed to set controller reference on runner", "runner", runnerName)
				continue
			}

			if err := r.Create(ctx, newRunner); err != nil {
				log.Error(err, "failed to create ephemeral runner", "runner", runnerName)
			}
		}
	} else if targetRunners < int32(len(activeRunners)) {
		// スケールダウン: Pending, WaitingForCluster, Idle のみ削除可能
		diff := int(int32(len(activeRunners)) - targetRunners)
		deleted := 0
		for _, run := range activeRunners {
			if deleted >= diff {
				break
			}
			if run.Status.Phase == ghav1alpha1.EphemeralRunnerPhasePending ||
				run.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster ||
				run.Status.Phase == ghav1alpha1.EphemeralRunnerPhaseIdle {
				log.Info("scaling down surplus ephemeral runner", "runner", run.Name)
				if r.Recorder != nil {
					r.Recorder.Eventf(ss, corev1.EventTypeNormal, "ScalingDown", "Scaling down surplus ephemeral runner %s", run.Name)
				}
				if err := r.Delete(ctx, run); err == nil {
					deleted++
				}
			}
		}
	}

	return nil
}

func (r *RunnerScaleSetReconciler) getGitHubClient(ctx context.Context, ss *ghav1alpha1.RunnerScaleSet) (githubscaleset.ScaleSetClient, error) {
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

func (r *RunnerScaleSetReconciler) getNodePoolDeclaredCapacity(ctx context.Context, nodePool *ghav1alpha1.RunnerNodePool) int32 {
	selector, err := metav1.LabelSelectorAsSelector(&nodePool.Spec.MachineSelector)
	if err != nil {
		return 0
	}
	var machineList ghav1alpha1.RunnerMachineList
	if err := r.List(ctx, &machineList, client.InNamespace(nodePool.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return 0
	}
	var total int32
	for _, m := range machineList.Items {
		total += m.Spec.Capacity.Runners
	}
	return total
}

func (r *RunnerScaleSetReconciler) findScaleSetsForNodePool(ctx context.Context, obj client.Object) []ctrl.Request {
	nodePool, ok := obj.(*ghav1alpha1.RunnerNodePool)
	if !ok {
		return nil
	}

	var scaleSets ghav1alpha1.RunnerScaleSetList
	if err := r.List(ctx, &scaleSets, client.InNamespace(nodePool.Namespace), client.MatchingFields{
		IndexNodePoolRefName: nodePool.Name,
	}); err != nil {
		// インデックスが未登録の場合はフォールバック
		if err := r.List(ctx, &scaleSets, client.InNamespace(nodePool.Namespace)); err != nil {
			return nil
		}
		var requests []ctrl.Request
		for _, ss := range scaleSets.Items {
			if ss.Spec.NodePoolRef.Name == nodePool.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Namespace: ss.Namespace,
						Name:      ss.Name,
					},
				})
			}
		}
		return requests
	}

	var requests []ctrl.Request
	for _, ss := range scaleSets.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: client.ObjectKey{
				Namespace: ss.Namespace,
				Name:      ss.Name,
			},
		})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *RunnerScaleSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ghav1alpha1.RunnerScaleSet{}).
		Owns(&ghav1alpha1.EphemeralRunner{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&coordinationv1.Lease{}).
		Watches(&ghav1alpha1.RunnerNodePool{}, handler.EnqueueRequestsFromMapFunc(r.findScaleSetsForNodePool)).
		Complete(r)
}
