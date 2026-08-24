package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/capacity"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/conditions"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/remotecluster"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/runner"
)

// RunnerNodePoolReconciler reconciles a RunnerNodePool object.
type RunnerNodePoolReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	Recorder        events.EventRecorder
	RemoteProvider  remotecluster.Provider
	Planner         capacity.MachineSelector
	EnableMultiNode bool
}

// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnernodepools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnernodepools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnernodepools/finalizers,verbs=update
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnermachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnermachines/status,verbs=get
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnerscalesets,verbs=get;list;watch
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=ephemeralrunners,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *RunnerNodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var nodePool ghav1alpha1.RunnerNodePool
	if err := r.Get(ctx, req.NamespacedName, &nodePool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	origNodePool := nodePool.DeepCopy()
	nodePool.Status.ObservedGeneration = nodePool.Generation

	// 1. 紐づくRunnerClusterを取得
	cluster, res := r.getReferencedCluster(ctx, &nodePool, origNodePool)
	if res != nil {
		return *res, nil
	}

	// 2. このNodePoolに所属するRunnerMachine一覧を取得 (nodePoolRef.Name)
	machineList, err := r.listPoolMachines(ctx, &nodePool, origNodePool)
	if err != nil {
		return ctrl.Result{}, err
	}

	// 3. このNodePoolを参照するRunnerScaleSet一覧を取得し、需要（Active/Pending/Desired）を集約
	activeRunnersByNode, totalActiveRunners, totalPendingRunners, totalDesiredRunners, err := r.aggregateDemand(ctx, &nodePool)
	if err != nil {
		log.Error(err, "failed to aggregate demand for nodepool")
		if updateErr := r.updateStatus(ctx, &nodePool, origNodePool); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{}, err
	}

	nodePool.Status.ActiveRunners = totalActiveRunners
	nodePool.Status.PendingRunners = totalPendingRunners

	// 4. 各MachineのStatusを集計
	prevDesiredMap := r.buildPrevDesiredMap(nodePool.Status.DesiredMachines)
	machineStatuses, poweredOnCount, readyNodesCount := r.collectMachineStatuses(cluster, machineList.Items, prevDesiredMap, activeRunnersByNode)

	nodePool.Status.PoweredOnNodes = poweredOnCount
	nodePool.Status.ReadyNodes = readyNodesCount

	// 5. スケールアップ要否の判定 (Unschedulable Podが存在、またはCold StartでReadyノードが0台)
	needsScaleUp := totalPendingRunners > 0 || (totalDesiredRunners > 0 && readyNodesCount == 0)

	// MachineSelectorで起動マシンを選択
	plan := r.Planner.Select(machineStatuses, needsScaleUp)
	if earlyRes, shouldReturn := r.handlePlanViolations(ctx, &nodePool, origNodePool, plan); shouldReturn {
		return earlyRes, nil
	}

	// 6. DesiredMachines計画の計算 (マシンごとの Opportunistic Scale-Down を反映)
	r.updateDesiredMachinesPlan(&nodePool, plan, machineList.Items, activeRunnersByNode, totalPendingRunners, totalDesiredRunners)
	nodePool.Status.DesiredNodes = int32(r.countActiveDesiredNodes(nodePool.Status.DesiredMachines))

	// 7. Status condition更新
	r.updateNodePoolConditions(&nodePool, plan, machineList.Items, totalActiveRunners, totalPendingRunners, totalDesiredRunners)

	// Status patch
	if err := r.updateStatus(ctx, &nodePool, origNodePool); err != nil {
		log.Error(err, "failed to patch runner node pool status")
		return ctrl.Result{}, err
	}

	// メトリクス更新
	metrics.DesiredNodes.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(float64(nodePool.Status.DesiredNodes))
	metrics.PoweredOnNodes.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(float64(nodePool.Status.PoweredOnNodes))
	metrics.ReadyNodes.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(float64(nodePool.Status.ReadyNodes))

	// Requeue
	requeueAfter := 30 * time.Second
	if (nodePool.Status.DesiredNodes > nodePool.Status.ReadyNodes && needsScaleUp) || totalPendingRunners > 0 {
		requeueAfter = 10 * time.Second
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *RunnerNodePoolReconciler) getReferencedCluster(ctx context.Context, nodePool, origNodePool *ghav1alpha1.RunnerNodePool) (*ghav1alpha1.RunnerCluster, *ctrl.Result) {
	log := logf.FromContext(ctx)
	var cluster ghav1alpha1.RunnerCluster
	if err := r.Get(ctx, client.ObjectKey{Namespace: nodePool.Namespace, Name: nodePool.Spec.ClusterRef.Name}, &cluster); err != nil {
		log.Error(err, "failed to get cluster for nodepool", "cluster", nodePool.Spec.ClusterRef.Name)
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, fmt.Sprintf("Cluster %s not found: %v", nodePool.Spec.ClusterRef.Name, err))
		if updateErr := r.updateStatus(ctx, nodePool, origNodePool); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		res := ctrl.Result{RequeueAfter: 15 * time.Second}
		return nil, &res
	}
	return &cluster, nil
}

func (r *RunnerNodePoolReconciler) listPoolMachines(ctx context.Context, nodePool, origNodePool *ghav1alpha1.RunnerNodePool) (*ghav1alpha1.RunnerMachineList, error) {
	log := logf.FromContext(ctx)
	var machineList ghav1alpha1.RunnerMachineList
	if err := r.List(ctx, &machineList, client.InNamespace(nodePool.Namespace), client.MatchingFields{
		IndexMachineNodePoolRefName: nodePool.Name,
	}); err != nil {
		if err := r.List(ctx, &machineList, client.InNamespace(nodePool.Namespace)); err != nil {
			log.Error(err, "failed to list runner machines for nodepool")
			if updateErr := r.updateStatus(ctx, nodePool, origNodePool); updateErr != nil {
				log.Error(updateErr, "failed to update status")
			}
			return nil, err
		}
	}
	return &machineList, nil
}

func (r *RunnerNodePoolReconciler) buildPrevDesiredMap(plans []ghav1alpha1.MachinePlanStatus) map[string]bool {
	prevDesiredMap := make(map[string]bool)
	for _, dm := range plans {
		if dm.DesiredState == ghav1alpha1.MachineDesiredStateActive {
			prevDesiredMap[dm.Name] = true
			if dm.UID != "" {
				prevDesiredMap[dm.UID] = true
			}
		}
	}
	return prevDesiredMap
}

func (r *RunnerNodePoolReconciler) handlePlanViolations(ctx context.Context, nodePool, origNodePool *ghav1alpha1.RunnerNodePool, plan capacity.Plan) (ctrl.Result, bool) {
	log := logf.FromContext(ctx)
	if plan.MultiNodeViolated {
		if r.Recorder != nil {
			r.Recorder.Eventf(nodePool, nil, corev1.EventTypeWarning, "MultiNodeUnsupported", "Reconcile", "MultiNode capacity planning is not supported in v1")
		}
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonMultiNodeUnsupported, "MultiNode is unsupported in v1")
		if updateErr := r.updateStatus(ctx, nodePool, origNodePool); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, true
	}

	if plan.StartupUnavailable {
		if r.Recorder != nil {
			r.Recorder.Eventf(nodePool, nil, corev1.EventTypeWarning, "StartupUnavailable", "Reconcile", "Required startup machine is quarantined or under maintenance")
		}
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonStartupUnavailable, "Required startup machine is quarantined or under maintenance")
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeCapacityReady, metav1.ConditionFalse, conditions.ReasonStartupUnavailable, "Cluster prerequisite unavailable")
		if updateErr := r.updateStatus(ctx, nodePool, origNodePool); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, true
	}

	return ctrl.Result{}, false
}

func (r *RunnerNodePoolReconciler) updateNodePoolConditions(
	nodePool *ghav1alpha1.RunnerNodePool,
	plan capacity.Plan,
	machines []ghav1alpha1.RunnerMachine,
	totalActiveRunners, totalPendingRunners, totalDesiredRunners int32,
) {
	if plan.PoolExhausted {
		msg := fmt.Sprintf("All %d RunnerMachines are active but %d runner pod(s) remain unschedulable", len(machines), totalPendingRunners)
		if r.Recorder != nil {
			r.Recorder.Eventf(nodePool, nil, corev1.EventTypeWarning, conditions.ReasonPoolExhausted, "Reconcile", msg)
		}
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeCapacityReady, metav1.ConditionFalse, conditions.ReasonPoolExhausted, msg)
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonSchedulingBlocked, msg)
	} else if plan.NodesStarting {
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeCapacityReady, metav1.ConditionFalse, conditions.ReasonNodesStarting, "Machines are powering on or waiting for node readiness")
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "Machines are powering on or waiting for node readiness")
	} else if nodePool.Status.ReadyNodes >= nodePool.Status.DesiredNodes && nodePool.Status.DesiredNodes > 0 {
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonReady, "NodePool is ready")
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeCapacityReady, metav1.ConditionTrue, conditions.ReasonCapacitySufficient, "Sufficient runner capacity available")
	} else if totalActiveRunners == 0 && totalDesiredRunners == 0 {
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonIdle, "NodePool is idle with zero demand")
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeCapacityReady, metav1.ConditionTrue, conditions.ReasonCapacitySufficient, "NodePool is idle")
	} else {
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "Waiting for node readiness")
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeCapacityReady, metav1.ConditionFalse, conditions.ReasonNotReady, "Waiting for runner capacity")
	}
}

func (r *RunnerNodePoolReconciler) countActiveDesiredNodes(plans []ghav1alpha1.MachinePlanStatus) int {
	count := 0
	for _, p := range plans {
		if p.DesiredState == ghav1alpha1.MachineDesiredStateActive {
			count++
		}
	}
	return count
}

func (r *RunnerNodePoolReconciler) updateStatus(ctx context.Context, np, _ *ghav1alpha1.RunnerNodePool) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current ghav1alpha1.RunnerNodePool
		if err := r.Get(ctx, client.ObjectKeyFromObject(np), &current); err != nil {
			return err
		}
		orig := current.DeepCopy()
		current.Status = np.Status
		patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
		return r.Status().Patch(ctx, &current, patch)
	})
}

func (r *RunnerNodePoolReconciler) aggregateDemand(ctx context.Context, nodePool *ghav1alpha1.RunnerNodePool) (map[string]int, int32, int32, int32, error) {
	var scaleSetList ghav1alpha1.RunnerScaleSetList
	if err := r.List(ctx, &scaleSetList, client.InNamespace(nodePool.Namespace), client.MatchingFields{
		IndexNodePoolRefName: nodePool.Name,
	}); err != nil {
		if err := r.List(ctx, &scaleSetList, client.InNamespace(nodePool.Namespace)); err != nil {
			return nil, 0, 0, 0, err
		}
	}

	var (
		totalActiveRunners  int32
		totalPendingRunners int32
		totalDesiredRunners int32
	)
	activeRunnersByNode := make(map[string]int)

	for i := range scaleSetList.Items {
		ss := &scaleSetList.Items[i]
		if ss.Spec.NodePoolRef.Name != nodePool.Name {
			continue
		}

		var runnerList ghav1alpha1.EphemeralRunnerList
		if err := r.List(ctx, &runnerList, client.InNamespace(nodePool.Namespace), client.MatchingFields{
			IndexScaleSetRefName: ss.Name,
		}); err != nil {
			if err := r.List(ctx, &runnerList, client.InNamespace(nodePool.Namespace)); err != nil {
				return nil, 0, 0, 0, err
			}
		}

		for _, run := range runnerList.Items {
			matches := false
			if run.Labels[runner.LabelScaleSetUID] != "" {
				matches = run.Labels[runner.LabelScaleSetUID] == string(ss.UID)
			} else {
				matches = run.Spec.ScaleSetRef.Name == ss.Name
			}
			if matches && isRunnerNonTerminal(run.Status.Phase) {
				totalActiveRunners++
				if run.Status.RemotePod.NodeName != "" {
					activeRunnersByNode[run.Status.RemotePod.NodeName]++
				} else {
					// ノード未確定 = スケジューリング待ち / Pending
					totalPendingRunners++
				}
			}
		}

		// ERSet から desired replicas を集約
		var ersList ghav1alpha1.EphemeralRunnerSetList
		if err := r.List(ctx, &ersList, client.InNamespace(ss.Namespace), client.MatchingFields{
			IndexScaleSetRefName: ss.Name,
		}); err == nil {
			for _, ers := range ersList.Items {
				if ers.Spec.ScaleSetRef.Name == ss.Name && ers.Spec.Replicas != nil {
					totalDesiredRunners += *ers.Spec.Replicas
				}
			}
		}
	}

	return activeRunnersByNode, totalActiveRunners, totalPendingRunners, totalDesiredRunners, nil
}

func (r *RunnerNodePoolReconciler) collectMachineStatuses(
	cluster *ghav1alpha1.RunnerCluster,
	machines []ghav1alpha1.RunnerMachine,
	prevDesiredMap map[string]bool,
	activeRunnersByNode map[string]int,
) ([]capacity.MachineStatus, int32, int32) {
	machineStatuses := make([]capacity.MachineStatus, 0, len(machines))
	var (
		poweredOnCount  int32
		readyNodesCount int32
	)

	startupMap := make(map[string]bool)
	if cluster != nil && cluster.Spec.Startup != nil {
		for _, sRef := range cluster.Spec.Startup.MachineRefs {
			startupMap[sRef.Name] = true
		}
	}

	for i := range machines {
		m := &machines[i]
		isPoweredOn := m.Status.PowerState == ghav1alpha1.PowerStateOn
		isReady := m.Status.Kubernetes.Ready && isPoweredOn
		isQuarantined := m.Status.Quarantine != nil
		isMaintenance := m.Spec.Maintenance != nil && m.Spec.Maintenance.Enabled
		isAlwaysOn := m.Spec.PowerPolicy == ghav1alpha1.RunnerMachinePowerPolicyAlwaysOn
		isStartup := startupMap[m.Name]
		wasDesired := prevDesiredMap[m.Name] || (m.UID != "" && prevDesiredMap[string(m.UID)])
		activeCount := activeRunnersByNode[m.Spec.NodeName]

		if isPoweredOn && !isQuarantined && !isMaintenance {
			poweredOnCount++
		}
		if isReady && !isQuarantined && !isMaintenance {
			readyNodesCount++
		}

		machineStatuses = append(machineStatuses, capacity.MachineStatus{
			Machine:           m,
			Priority:          m.Spec.Priority,
			StartupRequired:   isStartup,
			AlwaysOn:          isAlwaysOn,
			PoweredOn:         isPoweredOn,
			Ready:             m.Status.Kubernetes.Ready,
			PowerManageable:   true,
			Quarantined:       isQuarantined,
			Maintenance:       isMaintenance,
			PreviouslyDesired: wasDesired,
			ActiveRunners:     activeCount,
		})
	}

	return machineStatuses, poweredOnCount, readyNodesCount
}

func (r *RunnerNodePoolReconciler) updateDesiredMachinesPlan(
	nodePool *ghav1alpha1.RunnerNodePool,
	plan capacity.Plan,
	machines []ghav1alpha1.RunnerMachine,
	activeRunnersByNode map[string]int,
	totalPendingRunners int32,
	totalDesiredRunners int32,
) {
	now := metav1.Now()

	// 全体需要のアイドルタイマー更新
	if totalDesiredRunners > 0 || totalPendingRunners > 0 {
		nodePool.Status.IdleSince = nil
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeIdle, metav1.ConditionFalse, conditions.ReasonActive, "Runner demand is active")
	} else {
		if nodePool.Status.IdleSince == nil {
			nowTime := metav1.Now()
			nodePool.Status.IdleSince = &nowTime
			if r.Recorder != nil {
				r.Recorder.Eventf(nodePool, nil, corev1.EventTypeNormal, "IdleTimerStarted", "Reconcile", "Runner demand dropped to zero, idle timer started")
			}
			conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeIdle, metav1.ConditionTrue, conditions.ReasonIdle, "No runner demand, idle timer started")
		}
	}

	// 既存のDesiredMachinesをマップ化（前回のDrainStartedAt, IdleSinceを引き継ぐため）
	prevPlanMap := make(map[string]ghav1alpha1.MachinePlanStatus)
	for _, dm := range nodePool.Status.DesiredMachines {
		prevPlanMap[dm.Name] = dm
	}

	selectedSet := make(map[string]bool)
	for _, m := range plan.SelectedMachines {
		selectedSet[m.Name] = true
	}

	scaleDownDelay := 10 * time.Minute
	if nodePool.Spec.Scaling.ScaleDownDelay != nil && nodePool.Spec.Scaling.ScaleDownDelay.Duration > 0 {
		scaleDownDelay = nodePool.Spec.Scaling.ScaleDownDelay.Duration
	}

	var newPlan []ghav1alpha1.MachinePlanStatus
	for _, m := range machines {
		activeCount := activeRunnersByNode[m.Spec.NodeName]
		isAlwaysOn := m.Spec.PowerPolicy == ghav1alpha1.RunnerMachinePowerPolicyAlwaysOn
		prev, hasPrev := prevPlanMap[m.Name]

		if selectedSet[m.Name] {
			// マシン単位の Opportunistic Scale-Down 判定
			// Unschedulable 需要がなく、このマシン上の実行中 Runner が 0 で、AlwaysOn でない場合
			if totalPendingRunners == 0 && activeCount == 0 && !isAlwaysOn {
				var idleSince *metav1.Time
				if hasPrev && prev.IdleSince != nil {
					idleSince = prev.IdleSince
				} else {
					idleSince = &now
				}

				// Idle 時間が scaleDownDelay を超えた場合は電源 Off (Scale-down)
				if time.Since(idleSince.Time) >= scaleDownDelay {
					var drainStartedAt *metav1.Time
					if hasPrev && prev.DrainStartedAt != nil {
						drainStartedAt = prev.DrainStartedAt
					} else {
						drainStartedAt = &now
					}

					newPlan = append(newPlan, ghav1alpha1.MachinePlanStatus{
						Name:           m.Name,
						UID:            string(m.UID),
						DesiredState:   ghav1alpha1.MachineDesiredStateOff,
						IdleSince:      nil,
						DrainStartedAt: drainStartedAt,
					})
					continue
				}

				// まだ scaleDownDelay 待機中
				newPlan = append(newPlan, ghav1alpha1.MachinePlanStatus{
					Name:           m.Name,
					UID:            string(m.UID),
					DesiredState:   ghav1alpha1.MachineDesiredStateActive,
					IdleSince:      idleSince,
					DrainStartedAt: nil,
				})
			} else {
				// 実行中 Runner が存在するか、Unschedulable 需要がある場合は Active を維持してアイドルタイマーをリセット
				newPlan = append(newPlan, ghav1alpha1.MachinePlanStatus{
					Name:           m.Name,
					UID:            string(m.UID),
					DesiredState:   ghav1alpha1.MachineDesiredStateActive,
					IdleSince:      nil,
					DrainStartedAt: nil,
				})
			}
		} else {
			// 選択されなかったマシン (Off)
			var drainStartedAt *metav1.Time
			if hasPrev && prev.DesiredState == ghav1alpha1.MachineDesiredStateOff && prev.DrainStartedAt != nil {
				drainStartedAt = prev.DrainStartedAt
			} else {
				drainStartedAt = &now
			}

			newPlan = append(newPlan, ghav1alpha1.MachinePlanStatus{
				Name:           m.Name,
				UID:            string(m.UID),
				DesiredState:   ghav1alpha1.MachineDesiredStateOff,
				IdleSince:      nil,
				DrainStartedAt: drainStartedAt,
			})
		}
	}

	nodePool.Status.DesiredMachines = newPlan
}

func isRunnerNonTerminal(phase ghav1alpha1.EphemeralRunnerPhase) bool {
	switch phase {
	case ghav1alpha1.EphemeralRunnerPhaseCompleted, ghav1alpha1.EphemeralRunnerPhaseFailed, ghav1alpha1.EphemeralRunnerPhaseDeleting:
		return false
	case ghav1alpha1.EphemeralRunnerPhasePending, ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster,
		ghav1alpha1.EphemeralRunnerPhaseProvisioning, ghav1alpha1.EphemeralRunnerPhaseStarting,
		ghav1alpha1.EphemeralRunnerPhaseIdle, ghav1alpha1.EphemeralRunnerPhaseBusy:
		return true
	}
	return true
}

func (r *RunnerNodePoolReconciler) findNodePoolsForMachine(ctx context.Context, obj client.Object) []ctrl.Request {
	m, ok := obj.(*ghav1alpha1.RunnerMachine)
	if !ok || m.Spec.NodePoolRef == nil || m.Spec.NodePoolRef.Name == "" {
		return nil
	}

	return []ctrl.Request{
		{
			Namespace: m.Namespace,
			Name:      m.Spec.NodePoolRef.Name,
		},
	}
}

func (r *RunnerNodePoolReconciler) findNodePoolsForScaleSet(ctx context.Context, obj client.Object) []ctrl.Request {
	ss, ok := obj.(*ghav1alpha1.RunnerScaleSet)
	if !ok {
		return nil
	}

	return []ctrl.Request{
		{
			Namespace: ss.Namespace,
			Name:      ss.Spec.NodePoolRef.Name,
		},
	}
}

func (r *RunnerNodePoolReconciler) findNodePoolsForRunner(ctx context.Context, obj client.Object) []ctrl.Request {
	run, ok := obj.(*ghav1alpha1.EphemeralRunner)
	if !ok {
		return nil
	}

	var ss ghav1alpha1.RunnerScaleSet
	if err := r.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: run.Spec.ScaleSetRef.Name}, &ss); err != nil {
		return nil
	}

	return []ctrl.Request{
		{
			Namespace: ss.Namespace,
			Name:      ss.Spec.NodePoolRef.Name,
		},
	}
}

func (r *RunnerNodePoolReconciler) findNodePoolsForCluster(ctx context.Context, obj client.Object) []ctrl.Request {
	cluster, ok := obj.(*ghav1alpha1.RunnerCluster)
	if !ok {
		return nil
	}

	var pools ghav1alpha1.RunnerNodePoolList
	if err := r.List(ctx, &pools, client.InNamespace(cluster.Namespace)); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, p := range pools.Items {
		if p.Spec.ClusterRef.Name == cluster.Name {
			requests = append(requests, ctrl.Request{
				Namespace: p.Namespace,
				Name:      p.Name,
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *RunnerNodePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ghav1alpha1.RunnerNodePool{}).
		Watches(&ghav1alpha1.RunnerMachine{}, handler.EnqueueRequestsFromMapFunc(r.findNodePoolsForMachine)).
		Watches(&ghav1alpha1.RunnerScaleSet{}, handler.EnqueueRequestsFromMapFunc(r.findNodePoolsForScaleSet)).
		Watches(&ghav1alpha1.EphemeralRunner{}, handler.EnqueueRequestsFromMapFunc(r.findNodePoolsForRunner)).
		Watches(&ghav1alpha1.RunnerCluster{}, handler.EnqueueRequestsFromMapFunc(r.findNodePoolsForCluster)).
		Complete(r)
}
