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
	Planner         capacity.Planner
	EnableMultiNode bool
}

// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnernodepools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnernodepools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnernodepools/finalizers,verbs=update
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnermachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnermachines/status,verbs=get
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnerscalesets,verbs=get;list;watch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=ephemeralrunners,verbs=get;list;watch
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
	var cluster ghav1alpha1.RunnerCluster
	if err := r.Get(ctx, client.ObjectKey{Namespace: nodePool.Namespace, Name: nodePool.Spec.ClusterRef.Name}, &cluster); err != nil {
		log.Error(err, "failed to get cluster for nodepool", "cluster", nodePool.Spec.ClusterRef.Name)
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, fmt.Sprintf("Cluster %s not found: %v", nodePool.Spec.ClusterRef.Name, err))
		if updateErr := r.updateStatus(ctx, &nodePool, origNodePool); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// 2. このNodePoolに所属するRunnerMachine一覧を取得 (nodePoolRef.Name)
	var machineList ghav1alpha1.RunnerMachineList
	if err := r.List(ctx, &machineList, client.InNamespace(nodePool.Namespace), client.MatchingFields{
		IndexMachineNodePoolRefName: nodePool.Name,
	}); err != nil {
		if err := r.List(ctx, &machineList, client.InNamespace(nodePool.Namespace)); err != nil {
			log.Error(err, "failed to list runner machines for nodepool")
			if updateErr := r.updateStatus(ctx, &nodePool, origNodePool); updateErr != nil {
				log.Error(updateErr, "failed to update status")
			}
			return ctrl.Result{}, err
		}
	}

	// 3. このNodePoolを参照するRunnerScaleSet一覧を取得し、需要を集約
	referencingScaleSets, allocInputs, activeRunnersByNode, totalRequiredCapacity, err := r.aggregateDemand(ctx, &nodePool)
	if err != nil {
		log.Error(err, "failed to aggregate demand for nodepool")
		if updateErr := r.updateStatus(ctx, &nodePool, origNodePool); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{}, err
	}

	nodePool.Status.DesiredRunnerCapacity = totalRequiredCapacity

	// 4. 各MachineのStatusを集計 (前回のDesiredMachinesからPreviouslyDesiredマップを作成)
	prevDesiredMap := make(map[string]bool)
	for _, dm := range nodePool.Status.DesiredMachines {
		if dm.DesiredState == ghav1alpha1.MachineDesiredStateActive {
			prevDesiredMap[dm.Name] = true
			if dm.UID != "" {
				prevDesiredMap[dm.UID] = true
			}
		}
	}
	machineCapacities, poweredOnCount, readyNodesCount, potentialCapacity, readyCapacity := r.collectMachineCapacities(&cluster, machineList.Items, prevDesiredMap, activeRunnersByNode)

	nodePool.Status.PoweredOnNodes = poweredOnCount
	nodePool.Status.ReadyNodes = readyNodesCount
	nodePool.Status.PotentialRunnerCapacity = potentialCapacity
	nodePool.Status.ReadyRunnerCapacity = readyCapacity

	// 5. CapacityPlannerで計画を計算
	plan := r.Planner.Plan(machineCapacities, int(totalRequiredCapacity))
	if plan.MultiNodeViolated {
		if r.Recorder != nil {
			r.Recorder.Eventf(&nodePool, nil, corev1.EventTypeWarning, "MultiNodeUnsupported", "Reconcile", "MultiNode capacity planning is not supported in v1")
		}
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonMultiNodeUnsupported, "MultiNode is unsupported in v1")
		if updateErr := r.updateStatus(ctx, &nodePool, origNodePool); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	if plan.StartupUnavailable {
		if r.Recorder != nil {
			r.Recorder.Eventf(&nodePool, nil, corev1.EventTypeWarning, "StartupUnavailable", "Reconcile", "Required startup machine is quarantined or under maintenance")
		}
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonStartupUnavailable, "Required startup machine is quarantined or under maintenance")
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeCapacityReady, metav1.ConditionFalse, conditions.ReasonStartupUnavailable, "Cluster prerequisite unavailable")
		if updateErr := r.updateStatus(ctx, &nodePool, origNodePool); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	nodePool.Status.DesiredNodes = int32(len(plan.SelectedMachines))

	// 6. DesiredMachines計画をStatusに反映
	r.updateDesiredMachinesPlan(&nodePool, plan, machineList.Items, totalRequiredCapacity)

	// 7. 各ScaleSetのEffectiveMaxRunnersをFair-share配分 (潜在可能容量 potentialCapacity を基準に配分してコールドスタートを可能にする)
	r.syncScaleSetAllocations(ctx, referencingScaleSets, allocInputs, potentialCapacity)

	// 8. Status condition更新
	if nodePool.Status.ReadyNodes >= nodePool.Status.DesiredNodes && nodePool.Status.DesiredNodes > 0 {
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonReady, "NodePool is ready")
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeCapacityReady, metav1.ConditionTrue, conditions.ReasonCapacitySufficient, "Sufficient runner capacity available")
	} else if totalRequiredCapacity == 0 {
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonIdle, "NodePool is idle with zero demand")
	} else {
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "Machines are powering on or waiting for node readiness")
		conditions.SetConditionWithGeneration(&nodePool.Status.Conditions, nodePool.Generation, conditions.TypeCapacityReady, metav1.ConditionFalse, conditions.ReasonCapacityExceeded, "Waiting for runner capacity")
	}

	// Status patch
	if err := r.updateStatus(ctx, &nodePool, origNodePool); err != nil {
		log.Error(err, "failed to patch runner node pool status")
		return ctrl.Result{}, err
	}

	// メトリクス更新
	metrics.DesiredNodes.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(float64(nodePool.Status.DesiredNodes))
	metrics.PoweredOnNodes.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(float64(nodePool.Status.PoweredOnNodes))
	metrics.ReadyNodes.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(float64(nodePool.Status.ReadyNodes))

	committedCapacity := int32(plan.TotalCapacity)
	metrics.CapacityDemand.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(float64(totalRequiredCapacity))
	metrics.CapacityCommitted.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(float64(committedCapacity))
	metrics.CapacityReady.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(float64(readyCapacity))

	deficit := float64(totalRequiredCapacity - readyCapacity)
	if deficit < 0 {
		deficit = 0
	}
	metrics.CapacityDeficit.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(deficit)

	uncommittedDeficit := float64(totalRequiredCapacity - committedCapacity)
	if uncommittedDeficit < 0 {
		uncommittedDeficit = 0
	}
	metrics.UncommittedDeficit.WithLabelValues(nodePool.Namespace, nodePool.Name).Set(uncommittedDeficit)

	// Requeue
	requeueAfter := 30 * time.Second
	if nodePool.Status.DesiredNodes > nodePool.Status.ReadyNodes && totalRequiredCapacity > 0 {
		requeueAfter = 10 * time.Second
	} else if nodePool.Status.IdleSince != nil && totalRequiredCapacity == 0 {
		requeueAfter = 15 * time.Second
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
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

func (r *RunnerNodePoolReconciler) aggregateDemand(ctx context.Context, nodePool *ghav1alpha1.RunnerNodePool) ([]*ghav1alpha1.RunnerScaleSet, []capacity.ScaleSetAllocationInput, map[string]int, int32, error) {
	var scaleSetList ghav1alpha1.RunnerScaleSetList
	if err := r.List(ctx, &scaleSetList, client.InNamespace(nodePool.Namespace), client.MatchingFields{
		IndexNodePoolRefName: nodePool.Name,
	}); err != nil {
		if err := r.List(ctx, &scaleSetList, client.InNamespace(nodePool.Namespace)); err != nil {
			return nil, nil, nil, 0, err
		}
	}

	var totalRequiredCapacity int32
	var referencingScaleSets []*ghav1alpha1.RunnerScaleSet
	var allocInputs []capacity.ScaleSetAllocationInput
	activeRunnersByNode := make(map[string]int)

	for i := range scaleSetList.Items {
		ss := &scaleSetList.Items[i]
		if ss.Spec.NodePoolRef.Name != nodePool.Name {
			continue
		}
		referencingScaleSets = append(referencingScaleSets, ss)

		var runnerList ghav1alpha1.EphemeralRunnerList
		if err := r.List(ctx, &runnerList, client.InNamespace(nodePool.Namespace), client.MatchingFields{
			IndexScaleSetRefName: ss.Name,
		}); err != nil {
			if err := r.List(ctx, &runnerList, client.InNamespace(nodePool.Namespace)); err != nil {
				return nil, nil, nil, 0, err
			}
		}

		var nonTerminalCount int32
		for _, run := range runnerList.Items {
			matches := false
			if run.Labels[runner.LabelScaleSetUID] != "" {
				matches = run.Labels[runner.LabelScaleSetUID] == string(ss.UID)
			} else {
				matches = run.Spec.ScaleSetRef.Name == ss.Name
			}
			if matches && isRunnerNonTerminal(run.Status.Phase) {
				nonTerminalCount++
				if run.Status.RemotePod.NodeName != "" {
					activeRunnersByNode[run.Status.RemotePod.NodeName]++
				}
			}
		}

		// ERSet から desired replicas を集約
		desiredRunners := int32(0)
		var ersList ghav1alpha1.EphemeralRunnerSetList
		if err := r.List(ctx, &ersList, client.InNamespace(ss.Namespace), client.MatchingFields{
			IndexScaleSetRefName: ss.Name,
		}); err == nil {
			for _, ers := range ersList.Items {
				if ers.Spec.ScaleSetRef.Name == ss.Name && ers.Spec.Replicas != nil {
					desiredRunners += *ers.Spec.Replicas
				}
			}
		}

		required := max(nonTerminalCount, desiredRunners)
		totalRequiredCapacity += required

		maxLimit := int32(0)
		if ss.Spec.Scaling.MaxRunners != nil {
			maxLimit = *ss.Spec.Scaling.MaxRunners
		}

		allocInputs = append(allocInputs, capacity.ScaleSetAllocationInput{
			Name:          ss.Name,
			HardCommitted: nonTerminalCount,
			Max:           maxLimit,
		})
	}

	return referencingScaleSets, allocInputs, activeRunnersByNode, totalRequiredCapacity, nil
}

func (r *RunnerNodePoolReconciler) syncScaleSetAllocations(ctx context.Context, scaleSets []*ghav1alpha1.RunnerScaleSet, allocInputs []capacity.ScaleSetAllocationInput, potentialCapacity int32) {
	log := logf.FromContext(ctx)
	allocations := capacity.AllocateScaleSetCapacity(potentialCapacity, allocInputs)
	for _, ss := range scaleSets {
		allocated, ok := allocations[ss.Name]
		if !ok {
			continue
		}
		if ss.Status.EffectiveMaxRunners != allocated {
			log.Info("updating EffectiveMaxRunners for RunnerScaleSet based on potential capacity", "scaleSet", ss.Name, "oldMax", ss.Status.EffectiveMaxRunners, "newMax", allocated)
			_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				var current ghav1alpha1.RunnerScaleSet
				if err := r.Get(ctx, client.ObjectKeyFromObject(ss), &current); err != nil {
					return err
				}
				orig := current.DeepCopy()
				current.Status.EffectiveMaxRunners = allocated
				patch := client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})
				return r.Status().Patch(ctx, &current, patch)
			})
		}
	}
}

func (r *RunnerNodePoolReconciler) collectMachineCapacities(cluster *ghav1alpha1.RunnerCluster, machines []ghav1alpha1.RunnerMachine, prevDesiredMap map[string]bool, activeRunnersByNode map[string]int) ([]capacity.MachineCapacity, int32, int32, int32, int32) {
	machineCapacities := make([]capacity.MachineCapacity, 0, len(machines))
	var (
		poweredOnCount    int32
		readyNodesCount   int32
		potentialCapacity int32
		readyCapacity     int32
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

		if !isQuarantined && !isMaintenance {
			potentialCapacity += m.Spec.Capacity.RunnerSlots
		}

		if isPoweredOn && !isQuarantined && !isMaintenance {
			poweredOnCount++
		}
		if isReady && !isQuarantined && !isMaintenance {
			readyNodesCount++
			readyCapacity += m.Spec.Capacity.RunnerSlots
		}

		machineCapacities = append(machineCapacities, capacity.MachineCapacity{
			Machine:           m,
			Capacity:          int(m.Spec.Capacity.RunnerSlots),
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

	return machineCapacities, poweredOnCount, readyNodesCount, potentialCapacity, readyCapacity
}

func (r *RunnerNodePoolReconciler) updateDesiredMachinesPlan(
	nodePool *ghav1alpha1.RunnerNodePool,
	plan capacity.Plan,
	machines []ghav1alpha1.RunnerMachine,
	totalRequiredCapacity int32,
) {
	now := metav1.Now()

	// 全体需要のアイドルタイマー更新
	if totalRequiredCapacity > 0 {
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

	// 既存のDesiredMachinesをマップ化（前回のDrainStartedAtを引き継ぐため）
	prevPlanMap := make(map[string]ghav1alpha1.MachinePlanStatus)
	for _, dm := range nodePool.Status.DesiredMachines {
		prevPlanMap[dm.Name] = dm
	}

	selectedSet := make(map[string]bool)
	for _, m := range plan.SelectedMachines {
		selectedSet[m.Name] = true
	}

	var newPlan []ghav1alpha1.MachinePlanStatus
	for _, m := range machines {
		if selectedSet[m.Name] {
			// Activeとして選択されたマシン
			newPlan = append(newPlan, ghav1alpha1.MachinePlanStatus{
				Name:           m.Name,
				UID:            string(m.UID),
				DesiredState:   ghav1alpha1.MachineDesiredStateActive,
				DrainStartedAt: nil,
			})
		} else {
			// Offとして選択されたマシン (Scale-downまたは休止)
			var drainStartedAt *metav1.Time
			if prev, exists := prevPlanMap[m.Name]; exists && prev.DesiredState == ghav1alpha1.MachineDesiredStateOff && prev.DrainStartedAt != nil {
				drainStartedAt = prev.DrainStartedAt
			} else {
				// 新たにスケールダウン対象になった時刻を記録
				drainStartedAt = &now
			}

			newPlan = append(newPlan, ghav1alpha1.MachinePlanStatus{
				Name:           m.Name,
				UID:            string(m.UID),
				DesiredState:   ghav1alpha1.MachineDesiredStateOff,
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
