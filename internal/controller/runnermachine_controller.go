package controller

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/conditions"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/redfish"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/remotecluster"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/runner"
)

// RunnerMachineReconciler reconciles a RunnerMachine object.
type RunnerMachineReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Recorder       events.EventRecorder
	RemoteProvider remotecluster.Provider
	RedfishFactory redfish.PowerControllerFactory
}

// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnermachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnermachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnermachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnernodepools,verbs=get;list;watch
// +kubebuilder:rbac:groups=sharc.walnuts.dev,resources=runnerclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *RunnerMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var machine ghav1alpha1.RunnerMachine
	if err := r.Get(ctx, req.NamespacedName, &machine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	origMachine := machine.DeepCopy()
	machine.Status.ObservedGeneration = machine.Generation

	// 0. Redfish Circuit Breaker 判定 (BMC無応答時のBackoff)
	var skipRedfish bool
	if machine.Status.RedfishHealth != nil && machine.Status.RedfishHealth.Circuit == ghav1alpha1.RedfishCircuitOpen {
		if machine.Status.RedfishHealth.NextProbeTime != nil && time.Now().Before(machine.Status.RedfishHealth.NextProbeTime.Time) {
			skipRedfish = true
		} else {
			// Probe許可 (HalfOpen)
			machine.Status.RedfishHealth.Circuit = ghav1alpha1.RedfishCircuitHalfOpen
		}
	}

	// 1. 所属するRunnerClusterを取得
	var cluster ghav1alpha1.RunnerCluster
	if err := r.Get(ctx, client.ObjectKey{Namespace: machine.Namespace, Name: machine.Spec.ClusterRef.Name}, &cluster); err != nil {
		log.Error(err, "failed to get cluster for machine", "cluster", machine.Spec.ClusterRef.Name)
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, fmt.Sprintf("Cluster %s not found: %v", machine.Spec.ClusterRef.Name, err))
		if updateErr := r.updateStatus(ctx, &machine, origMachine); updateErr != nil {
			log.Error(updateErr, "failed to update status")
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// 2. 所属するRunnerNodePoolからDesiredStateとDrain開始時刻を取得
	desiredState, drainStartedAt, nodePool := r.getDesiredState(ctx, &machine)

	// 3. Redfish BMCから最新の電源状態を観測
	pwrCtrl := r.observeRedfish(ctx, &machine, skipRedfish)

	// 4. リモートKubernetesクラスタのNode状態を観測
	remoteNode, earlyResult, err := r.observeRemoteNode(ctx, &machine, origMachine, &cluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	if earlyResult != nil {
		return *earlyResult, nil
	}

	// 5. Quarantine安定化判定・回復判定
	r.reconcileQuarantineRecovery(&machine)

	// 6. メンテナンスモードまたはDesiredStateに応じた電源操作・Cordon制御
	requeueAfter := 30 * time.Second
	if machine.Spec.Maintenance != nil && machine.Spec.Maintenance.Enabled {
		requeueAfter = r.reconcileMaintenance(ctx, &machine, &cluster, remoteNode, pwrCtrl)
	} else {
		conditions.RemoveCondition(&machine.Status.Conditions, conditions.TypeMaintenance)
		conditions.RemoveCondition(&machine.Status.Conditions, conditions.TypeMaintenanceReady)
		switch desiredState {
		case ghav1alpha1.MachineDesiredStateActive:
			requeueAfter = r.reconcileActive(ctx, &machine, &cluster, remoteNode, pwrCtrl)
		case ghav1alpha1.MachineDesiredStateOff:
			requeueAfter = r.reconcileOff(ctx, &machine, &cluster, nodePool, remoteNode, pwrCtrl, drainStartedAt)
		}
	}

	// 6. Overall Ready Condition の設定
	if machine.Spec.Maintenance != nil && machine.Spec.Maintenance.Enabled {
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonMaintenance, "Machine is under maintenance")
	} else if machine.Status.PowerState == ghav1alpha1.PowerStateOn && machine.Status.Kubernetes.Ready {
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonReady, "Machine is powered on and Node is Ready")
	} else if machine.Status.PowerState == ghav1alpha1.PowerStateOff && desiredState == ghav1alpha1.MachineDesiredStateOff {
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonScaledToZero, "Machine is powered off (scaled to zero)")
	} else {
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "Machine is not fully ready")
	}

	// メトリクス更新
	stateVal := 0.0
	if machine.Status.PowerState == ghav1alpha1.PowerStateOn {
		stateVal = 1.0
	}
	metrics.MachinePowerState.WithLabelValues(machine.Namespace, machine.Name, string(machine.Status.PowerState)).Set(stateVal)
	metrics.MachinePoweredOn.WithLabelValues(machine.Namespace, machine.Name).Set(stateVal)

	// Status更新
	if err := r.updateStatus(ctx, &machine, origMachine); err != nil {
		log.Error(err, "failed to patch runner machine status")
		return ctrl.Result{}, err
	}

	// Thundering herd 防止のため、決定論的 Jitter を付加
	if requeueAfter > 0 {
		requeueAfter += stableJitter(machine.UID, 5*time.Second)
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func stableJitter(uid types.UID, maxDelay time.Duration) time.Duration {
	if maxDelay <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(uid))
	return time.Duration(h.Sum64() % uint64(maxDelay))
}

func (r *RunnerMachineReconciler) observeRedfish(ctx context.Context, machine *ghav1alpha1.RunnerMachine, skipRedfish bool) redfish.PowerController {
	log := logf.FromContext(ctx)
	if skipRedfish {
		log.Info("skipping Redfish power state poll due to active circuit breaker backoff", "machine", machine.Name, "nextProbeTime", machine.Status.RedfishHealth.NextProbeTime)
		return nil
	}

	pwrCtrl, err := r.getRedfishController(ctx, machine)
	if err != nil {
		log.Error(err, "failed to create redfish controller")
		r.recordRedfishFailure(machine, time.Now(), err)
		return nil
	}

	state, err := pwrCtrl.GetPowerState(ctx)
	if err != nil {
		log.Error(err, "failed to get power state from redfish")
		r.recordRedfishFailure(machine, time.Now(), err)
		return pwrCtrl
	}

	r.recordRedfishSuccess(machine, time.Now())

	if machine.Status.Operation != nil && (machine.Status.Operation.Type == ghav1alpha1.PowerOperationTypeGracefulShutdown || machine.Status.Operation.Type == ghav1alpha1.PowerOperationTypeForceOff) {
		if state == ghav1alpha1.PowerStateOff {
			now := metav1.Now()
			machine.Status.LastPowerTransitionTime = &now
			machine.Status.PowerState = ghav1alpha1.PowerStateOff
			machine.Status.Operation = nil
		} else {
			// シャットダウン操作中（BMCがまだOnの場合）はコントローラー上の状態としてPoweringOffを維持
			machine.Status.PowerState = ghav1alpha1.PowerStatePoweringOff
		}
	} else {
		if machine.Status.PowerState != state {
			now := metav1.Now()
			machine.Status.LastPowerTransitionTime = &now
			machine.Status.PowerState = state
		}
		if machine.Status.Operation != nil && machine.Status.PowerState == ghav1alpha1.PowerStateOn && machine.Status.Operation.Type == ghav1alpha1.PowerOperationTypePowerOn {
			machine.Status.Operation = nil
		}
	}
	return pwrCtrl
}

func (r *RunnerMachineReconciler) observeRemoteNode(
	ctx context.Context,
	machine, origMachine *ghav1alpha1.RunnerMachine,
	cluster *ghav1alpha1.RunnerCluster,
) (*corev1.Node, *ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !cluster.Status.APIReachable {
		machine.Status.Kubernetes.Present = false
		machine.Status.Kubernetes.Ready = false
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeKubernetesNodeReady, metav1.ConditionFalse, conditions.ReasonAPIUnreachable, "Cluster API unreachable")
		return nil, nil, nil
	}

	node, err := r.RemoteProvider.GetNode(ctx, cluster, machine.Spec.NodeName)
	if err != nil {
		machine.Status.Kubernetes.Present = false
		machine.Status.Kubernetes.Ready = false
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeKubernetesNodeReady, metav1.ConditionFalse, conditions.ReasonNodeNotFound, fmt.Sprintf("Failed to get node: %v", err))
		return nil, nil, err
	}
	if node == nil {
		machine.Status.Kubernetes.Present = false
		machine.Status.Kubernetes.Ready = false
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeKubernetesNodeReady, metav1.ConditionFalse, conditions.ReasonNodeNotFound, "Kubernetes node not found")
		return nil, nil, nil
	}

	machine.Status.Kubernetes.Present = true
	currentUID := string(node.UID)
	currentMID := node.Status.NodeInfo.MachineID

	// 管理者による明示的な Adopt (sharc.walnuts.dev/adopt-machine-id アノテーション) の検証
	adoptMID, hasAdopt := machine.Annotations[runner.AnnotationAdoptMachineID]
	if hasAdopt && adoptMID == currentMID && currentMID != "" {
		log.Info("adopting new machine identity from annotation", "machine", machine.Name, "newMachineID", currentMID)
		machine.Status.Kubernetes.BoundMachineID = currentMID
		machine.Status.Kubernetes.MachineID = currentMID
		machine.Status.Kubernetes.NodeUID = currentUID
		machine.Status.Kubernetes.ObservedMachineID = currentMID
		if err := r.Status().Patch(ctx, machine, client.MergeFrom(origMachine)); err != nil {
			return nil, nil, err
		}
		updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var current ghav1alpha1.RunnerMachine
			if err := r.Get(ctx, client.ObjectKeyFromObject(machine), &current); err != nil {
				return err
			}
			delete(current.Annotations, runner.AnnotationAdoptMachineID)
			return r.Update(ctx, &current)
		})
		if updateErr != nil {
			return nil, nil, updateErr
		}
		res := ctrl.Result{RequeueAfter: 1 * time.Second}
		return nil, &res, nil
	}

	switch {
	case machine.Status.Kubernetes.BoundMachineID == "" && currentMID != "":
		// 初回バインディング
		machine.Status.Kubernetes.BoundMachineID = currentMID
		machine.Status.Kubernetes.MachineID = currentMID
		machine.Status.Kubernetes.NodeUID = currentUID
		machine.Status.Kubernetes.ObservedMachineID = currentMID
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeIdentityValid, metav1.ConditionTrue, conditions.ReasonReady, "Machine identity bound")

	case currentMID != "" && machine.Status.Kubernetes.BoundMachineID != currentMID:
		// MachineID mismatch: 期待値を上書きせず、IdentityValid=False に倒して mutation/capacity をブロック
		machine.Status.Kubernetes.ObservedMachineID = currentMID
		machine.Status.Kubernetes.Ready = false
		log.Error(nil, "node machineID mismatch, blocking machine actions until explicit adoption",
			"machine", machine.Name, "expected", machine.Status.Kubernetes.BoundMachineID, "actual", currentMID)
		if r.Recorder != nil {
			r.Recorder.Eventf(machine, nil, corev1.EventTypeWarning, "MachineIDMismatch", "Reconcile",
				"Node %s has different machineID %s (expected %s); mutation blocked until explicit adoption",
				node.Name, currentMID, machine.Status.Kubernetes.BoundMachineID)
		}
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeIdentityValid, metav1.ConditionFalse,
			conditions.ReasonMachineIDMismatch, fmt.Sprintf("expected machineID %q, observed %q", machine.Status.Kubernetes.BoundMachineID, currentMID))
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeKubernetesNodeReady, metav1.ConditionFalse,
			conditions.ReasonMachineIDMismatch, "Node machine identity mismatch")
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeReady, metav1.ConditionFalse,
			conditions.ReasonMachineIDMismatch, "Node identity invalid")
		if updateErr := r.updateStatus(ctx, machine, origMachine); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return nil, nil, updateErr
		}
		res := ctrl.Result{RequeueAfter: 1 * time.Minute}
		return nil, &res, nil

	default:
		// 同一 MachineID: Node 再作成の場合は NodeUID のみを更新
		machine.Status.Kubernetes.NodeUID = currentUID
		machine.Status.Kubernetes.ObservedMachineID = currentMID
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeIdentityValid, metav1.ConditionTrue, conditions.ReasonReady, "Machine identity valid")
	}

	machine.Status.Kubernetes.Ready = remotecluster.IsNodeReady(node)
	if machine.Status.Kubernetes.Ready {
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeKubernetesNodeReady, metav1.ConditionTrue, conditions.ReasonNodeReady, "Kubernetes node is Ready")
	} else {
		conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeKubernetesNodeReady, metav1.ConditionFalse, conditions.ReasonNodeNotReady, "Kubernetes node is NotReady")
		// 電源ON状態が継続しているのにNodeがReadyにならない場合、Quarantine判定
		nodeReadyTimeout := 10 * time.Minute
		if cluster.Spec.Readiness.NodeReadyTimeout != nil && cluster.Spec.Readiness.NodeReadyTimeout.Duration > 0 {
			nodeReadyTimeout = cluster.Spec.Readiness.NodeReadyTimeout.Duration
		}
		if machine.Status.PowerState == ghav1alpha1.PowerStateOn && machine.Status.LastPowerTransitionTime != nil {
			if time.Since(machine.Status.LastPowerTransitionTime.Time) > nodeReadyTimeout && machine.Status.Quarantine == nil {
				now := metav1.Now()
				machine.Status.Quarantine = &ghav1alpha1.MachineQuarantineStatus{
					Reason:              "NodeReadyTimeout",
					Since:               now,
					ConsecutiveFailures: 1,
				}
				conditions.SetConditionWithGeneration(&machine.Status.Conditions, machine.Generation, conditions.TypeQuarantined, metav1.ConditionTrue, conditions.ReasonQuarantined, fmt.Sprintf("Node failed to become Ready within %s", nodeReadyTimeout))
				if r.Recorder != nil {
					r.Recorder.Eventf(machine, nil, corev1.EventTypeWarning, "MachineQuarantined", "Reconcile", "Machine %s quarantined: Node failed to become Ready within %s", machine.Name, nodeReadyTimeout)
				}
			}
		}
	}
	return node, nil, nil
}

type CordonOwnership int

const (
	CordonNotNeeded CordonOwnership = iota
	CordonOwnedBySHARC
	CordonExternal
)

func (r *RunnerMachineReconciler) ensureCordoned(
	ctx context.Context,
	c client.Client,
	node *corev1.Node,
	machineUID types.UID,
) (CordonOwnership, error) {
	annotations := node.GetAnnotations()
	owner := ""
	if annotations != nil {
		owner = annotations[runner.AnnotationCordonedBy]
	}

	// 既にSHARCによってCordonされている場合
	if node.Spec.Unschedulable && owner == string(machineUID) {
		return CordonOwnedBySHARC, nil
	}

	// 外部（管理者や他ツール）によって既にCordonされている場合: 所有権は奪わない
	if node.Spec.Unschedulable {
		return CordonExternal, nil
	}

	// SHARCがSchedulable -> Unschedulableへ遷移させ、所有権アノテーションを付与
	orig := node.DeepCopy()
	node.Spec.Unschedulable = true
	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	node.Annotations[runner.AnnotationCordonedBy] = string(machineUID)

	if err := c.Patch(ctx, node, client.MergeFrom(orig)); err != nil {
		return CordonNotNeeded, err
	}

	return CordonOwnedBySHARC, nil
}

func (r *RunnerMachineReconciler) reconcileMaintenance(
	ctx context.Context,
	m *ghav1alpha1.RunnerMachine,
	cluster *ghav1alpha1.RunnerCluster,
	remoteNode *corev1.Node,
	pwrCtrl redfish.PowerController,
) time.Duration {
	log := logf.FromContext(ctx)
	log.Info("machine is in maintenance mode", "machine", m.Name)
	conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeMaintenance, metav1.ConditionTrue, conditions.ReasonMaintenance, "Machine is under maintenance")

	// 1. ノードを Cordon して新規ジョブ割り当てを阻止
	if remoteNode != nil {
		if remoteClient, clientErr := r.RemoteProvider.GetClient(ctx, cluster); clientErr == nil {
			_, _ = r.ensureCordoned(ctx, remoteClient, remoteNode, m.UID)
		}
	}

	// 2. 実行中 Pod を確認
	activePods, err := r.countActiveRunnerPodsOnNode(ctx, cluster, m.Spec.NodeName)
	if err != nil {
		log.Error(err, "failed to count active runner pods during maintenance reconciliation")
		return 5 * time.Second
	}

	if activePods > 0 {
		log.Info("waiting for active runner pods to finish before maintenance ready", "machine", m.Name, "activePods", activePods)
		conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeMaintenanceReady, metav1.ConditionFalse, conditions.ReasonDraining, fmt.Sprintf("Waiting for %d runner pods to finish", activePods))
		return 5 * time.Second
	}

	// 3. Pod = 0 に到達後、PowerPolicy に応じた処理
	powerPolicy := ghav1alpha1.MaintenancePowerPolicyPreserve
	if m.Spec.Maintenance != nil && m.Spec.Maintenance.PowerPolicy != "" {
		powerPolicy = m.Spec.Maintenance.PowerPolicy
	}

	if powerPolicy == ghav1alpha1.MaintenancePowerPolicyPowerOff && m.Status.PowerState == ghav1alpha1.PowerStateOn {
		if m.Status.Operation == nil || m.Status.Operation.Type != ghav1alpha1.PowerOperationTypeGracefulShutdown {
			r.initiateGracefulShutdown(ctx, m, pwrCtrl)
		}
		conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeMaintenanceReady, metav1.ConditionFalse, conditions.ReasonPowerTransitioning, "Powering off machine for maintenance")
		return 5 * time.Second
	}

	// 4. Maintenance Ready 完了
	conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeMaintenanceReady, metav1.ConditionTrue, "Ready", "Machine is drained and ready for maintenance")
	return 1 * time.Minute
}

func (r *RunnerMachineReconciler) reconcileActive(
	ctx context.Context,
	m *ghav1alpha1.RunnerMachine,
	cluster *ghav1alpha1.RunnerCluster,
	remoteNode *corev1.Node,
	pwrCtrl redfish.PowerController,
) time.Duration {
	log := logf.FromContext(ctx)

	// 1. Shutdown Commit Point以降の方向転換保護: シャットダウン処理中の場合はまずOffになるのを待つ
	if m.Status.Operation != nil && (m.Status.Operation.Type == ghav1alpha1.PowerOperationTypeGracefulShutdown || m.Status.Operation.Type == ghav1alpha1.PowerOperationTypeForceOff) {
		if m.Status.PowerState != ghav1alpha1.PowerStateOff {
			shutdownTimeout := 3 * time.Minute
			if m.Spec.Redfish.Power.Shutdown.Timeout != nil && m.Spec.Redfish.Power.Shutdown.Timeout.Duration > 0 {
				shutdownTimeout = m.Spec.Redfish.Power.Shutdown.Timeout.Duration
			}
			timeoutPolicy := ghav1alpha1.RedfishTimeoutPolicyAbort
			if m.Spec.Redfish.Power.Shutdown.TimeoutPolicy != "" {
				timeoutPolicy = m.Spec.Redfish.Power.Shutdown.TimeoutPolicy
			}

			if !m.Status.Operation.StartedAt.IsZero() && time.Since(m.Status.Operation.StartedAt.Time) > shutdownTimeout {
				log.Info("in-flight shutdown exceeded timeout during active reconciliation", "machine", m.Name, "policy", timeoutPolicy)
				r.handleShutdownTimeout(ctx, m, pwrCtrl, shutdownTimeout, timeoutPolicy)
				if m.Status.Operation == nil {
					// Abort policy cleared the operation, continue active reconciliation
				} else if m.Status.PowerState != ghav1alpha1.PowerStateOff {
					return 5 * time.Second
				}
			} else {
				log.Info("waiting for in-flight shutdown to complete before restarting machine", "machine", m.Name, "operation", m.Status.Operation.Type)
				return 5 * time.Second
			}
		}
		// OffになったらOperationをクリアしてPowerOnへ進む
		m.Status.Operation = nil
	}

	// 2. もし電源OFFならPowerOnを実行
	if m.Status.PowerState == ghav1alpha1.PowerStateOff {
		observationGrace := 60 * time.Second
		if m.Status.Operation != nil && m.Status.Operation.Type == ghav1alpha1.PowerOperationTypePowerOn {
			elapsed := time.Since(m.Status.Operation.LastAttemptAt.Time)
			if elapsed < observationGrace {
				log.Info("within PowerOn observation grace period; observing without issuing duplicate command", "machine", m.Name, "elapsed", elapsed, "grace", observationGrace)
				return 10 * time.Second
			}

			// ObservationGrace経過後もOffのまま試行回数が3回以上の場合、Quarantine判定
			if m.Status.Operation.Attempts >= 3 {
				log.Info("machine failed to power on after multiple attempts; putting into quarantine", "machine", m.Name, "attempts", m.Status.Operation.Attempts)
				now := metav1.Now()
				m.Status.Quarantine = &ghav1alpha1.MachineQuarantineStatus{
					Reason:              "PowerOnNotObserved",
					Since:               now,
					ConsecutiveFailures: m.Status.Operation.Attempts,
				}
				conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeQuarantined, metav1.ConditionTrue, "PowerOnNotObserved", fmt.Sprintf("Machine failed to reach PoweringOn/On state after %d PowerOn attempts", m.Status.Operation.Attempts))
				return 1 * time.Minute
			}
		}

		log.Info("powering on machine", "machine", m.Name)
		if r.Recorder != nil {
			r.Recorder.Eventf(m, nil, corev1.EventTypeNormal, "PoweringOn", "PowerOn", "Powering on machine %s", m.Name)
		}

		now := metav1.Now()
		attempts := int32(1)
		startedAt := now
		if m.Status.Operation != nil && m.Status.Operation.Type == ghav1alpha1.PowerOperationTypePowerOn {
			attempts = m.Status.Operation.Attempts + 1
			startedAt = m.Status.Operation.StartedAt
		}
		m.Status.Operation = &ghav1alpha1.PowerOperationStatus{
			Type:          ghav1alpha1.PowerOperationTypePowerOn,
			StartedAt:     startedAt,
			LastAttemptAt: now,
			Attempts:      attempts,
		}

		if pwrCtrl != nil {
			if err := pwrCtrl.PowerOn(ctx); err != nil {
				log.Error(err, "failed to power on machine", "machine", m.Name)
				if r.Recorder != nil {
					r.Recorder.Eventf(m, nil, corev1.EventTypeWarning, "PowerOnFailed", "PowerOn", "Failed to power on machine %s: %v", m.Name, err)
				}
			} else {
				metrics.PowerTransitionsTotal.WithLabelValues(m.Namespace, m.Name, "PowerOn").Inc(ctx)
			}
		}
		return 10 * time.Second
	}

	// 3. 起動中なら待機
	if m.Status.PowerState == ghav1alpha1.PowerStatePoweringOn {
		return 10 * time.Second
	}

	// 4. 電源ONかつNodeがReadyなら、自身が設定したCordonのみ解除（外部Cordonは保護）
	r.ensureUncordonIfReady(ctx, m, cluster, remoteNode)

	return 30 * time.Second
}

func (r *RunnerMachineReconciler) ensureUncordonIfReady(ctx context.Context, m *ghav1alpha1.RunnerMachine, cluster *ghav1alpha1.RunnerCluster, remoteNode *corev1.Node) {
	log := logf.FromContext(ctx)
	if m.Status.PowerState != ghav1alpha1.PowerStateOn || remoteNode == nil || !m.Status.Kubernetes.Ready {
		return
	}
	if remoteNode.Spec.Unschedulable && remoteNode.Annotations != nil && remoteNode.Annotations[runner.AnnotationCordonedBy] == string(m.UID) {
		remoteClient, err := r.RemoteProvider.GetClient(ctx, cluster)
		if err == nil {
			origNode := remoteNode.DeepCopy()
			remoteNode.Spec.Unschedulable = false
			delete(remoteNode.Annotations, runner.AnnotationCordonedBy)
			if patchErr := remoteClient.Patch(ctx, remoteNode, client.MergeFrom(origNode)); patchErr == nil {
				log.Info("uncordoned node after machine became ready", "node", remoteNode.Name, "machine", m.Name)
			}
		}
	}
}

func (r *RunnerMachineReconciler) reconcileQuarantineRecovery(m *ghav1alpha1.RunnerMachine) {
	log := logf.Log.WithName("quarantine-recovery")
	if m.Status.Quarantine == nil {
		return
	}
	if m.Status.Quarantine.Reason == conditions.ReasonMachineIDMismatch {
		log.Info("machine is quarantined due to MachineIDMismatch; automatic recovery is blocked (requires explicit annotation)", "machine", m.Name)
		return
	}
	if m.Status.Quarantine.Reason == "PowerOnNotObserved" && (m.Status.PowerState == ghav1alpha1.PowerStateOn || m.Status.PowerState == ghav1alpha1.PowerStatePoweringOn) {
		log.Info("clearing PowerOnNotObserved quarantine since machine power on was observed", "machine", m.Name, "powerState", m.Status.PowerState)
		m.Status.Quarantine = nil
		conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeQuarantined, metav1.ConditionFalse, conditions.ReasonReady, "Machine power on observed")
		return
	}
	if m.Status.Kubernetes.Ready {
		now := metav1.Now()
		if m.Status.Quarantine.HealthySince == nil {
			m.Status.Quarantine.HealthySince = &now
		} else if time.Since(m.Status.Quarantine.HealthySince.Time) >= 10*time.Minute {
			log.Info("clearing quarantine after continuous 10m stabilization period", "machine", m.Name)
			m.Status.Quarantine = nil
			conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeQuarantined, metav1.ConditionFalse, conditions.ReasonReady, "Machine is stable and healthy")
		}
	} else {
		m.Status.Quarantine.HealthySince = nil
	}
}

func (r *RunnerMachineReconciler) reconcileOff(
	ctx context.Context,
	m *ghav1alpha1.RunnerMachine,
	cluster *ghav1alpha1.RunnerCluster,
	nodePool *ghav1alpha1.RunnerNodePool,
	remoteNode *corev1.Node,
	pwrCtrl redfish.PowerController,
	drainStartedAt *metav1.Time,
) time.Duration {
	log := logf.FromContext(ctx)

	// AlwaysOn マシンはスケールダウンによる電源OFFを禁止
	if m.Spec.PowerPolicy == ghav1alpha1.RunnerMachinePowerPolicyAlwaysOn {
		log.Info("machine powerPolicy is AlwaysOn, preserving power", "machine", m.Name)
		return 1 * time.Minute
	}

	// Quarantine 中のマシンは原因調査・デバッグのため電源状態を維持 (PreservePower)
	if m.Status.Quarantine != nil {
		log.Info("machine is quarantined, preserving power state for inspection", "machine", m.Name, "reason", m.Status.Quarantine.Reason)
		return 1 * time.Minute
	}

	// 1. 既に電源OFF
	if m.Status.PowerState == ghav1alpha1.PowerStateOff {
		return 1 * time.Minute
	}

	shutdownTimeout := 3 * time.Minute
	if m.Spec.Redfish.Power.Shutdown.Timeout != nil && m.Spec.Redfish.Power.Shutdown.Timeout.Duration > 0 {
		shutdownTimeout = m.Spec.Redfish.Power.Shutdown.Timeout.Duration
	}

	timeoutPolicy := ghav1alpha1.RedfishTimeoutPolicyAbort
	if m.Spec.Redfish.Power.Shutdown.TimeoutPolicy != "" {
		timeoutPolicy = m.Spec.Redfish.Power.Shutdown.TimeoutPolicy
	}

	// 2. シャットダウン操作中 (Operation == GracefulShutdown / ForceOff または PoweringOff) のタイムアウト・ForceOff判定
	if (m.Status.Operation != nil && (m.Status.Operation.Type == ghav1alpha1.PowerOperationTypeGracefulShutdown || m.Status.Operation.Type == ghav1alpha1.PowerOperationTypeForceOff)) || m.Status.PowerState == ghav1alpha1.PowerStatePoweringOff {
		r.handleShutdownTimeout(ctx, m, pwrCtrl, shutdownTimeout, timeoutPolicy)
		return 10 * time.Second
	}

	// 3. 電源ONの場合: Drainingおよび安全なスケールダウン判定
	if m.Status.PowerState == ghav1alpha1.PowerStateOn {
		return r.reconcilePowerOffForRunningMachine(ctx, m, cluster, nodePool, remoteNode, pwrCtrl, drainStartedAt)
	}

	return 30 * time.Second
}

func (r *RunnerMachineReconciler) reconcilePowerOffForRunningMachine(
	ctx context.Context,
	m *ghav1alpha1.RunnerMachine,
	cluster *ghav1alpha1.RunnerCluster,
	nodePool *ghav1alpha1.RunnerNodePool,
	remoteNode *corev1.Node,
	pwrCtrl redfish.PowerController,
	drainStartedAt *metav1.Time,
) time.Duration {
	log := logf.FromContext(ctx)

	// 3.1 NodeをCordonして新規Podのスケジュールを阻止（外部Cordonは所有権を奪わず検知）
	if cluster.Status.APIReachable && remoteNode != nil {
		remoteClient, clientErr := r.RemoteProvider.GetClient(ctx, cluster)
		if clientErr == nil {
			ownership, cordonErr := r.ensureCordoned(ctx, remoteClient, remoteNode, m.UID)
			if cordonErr != nil {
				log.Error(cordonErr, "failed to ensure node cordoned", "node", remoteNode.Name)
			}
			if ownership == CordonExternal {
				m.Status.ExternallyCordoned = true
				log.Info("node is cordoned by external actor, blocking scale-down power off", "node", remoteNode.Name)
				conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonExternalCordon, "Node is cordoned externally; scale down power off is blocked")
				return 30 * time.Second
			}
			m.Status.ExternallyCordoned = false
		}
	}

	// 3.2 このNode上で現在実行中のRunner Podが存在するか確認 (Fail closed)
	activePodCount, countErr := r.countActiveRunnerPodsOnNode(ctx, cluster, m.Spec.NodeName)
	if countErr != nil {
		log.Error(countErr, "failed to count active runner pods on node, retrying safely", "machine", m.Name)
		return 5 * time.Second
	}
	if activePodCount > 0 {
		drainTimeout := 10 * time.Minute
		if m.Spec.Drain != nil && m.Spec.Drain.Timeout != nil && m.Spec.Drain.Timeout.Duration > 0 {
			drainTimeout = m.Spec.Drain.Timeout.Duration
		} else if nodePool != nil && nodePool.Spec.Drain != nil && nodePool.Spec.Drain.Timeout != nil && nodePool.Spec.Drain.Timeout.Duration > 0 {
			drainTimeout = nodePool.Spec.Drain.Timeout.Duration
		}

		if drainStartedAt != nil && time.Since(drainStartedAt.Time) > drainTimeout {
			log.Info("drain timed out with active runner pods; blocking scale-down power off to protect running jobs", "machine", m.Name, "activePods", activePodCount, "timeout", drainTimeout)
			conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeReady, metav1.ConditionFalse, "DrainTimedOut", fmt.Sprintf("Drain timed out after %s with %d active pods; scale-down power off blocked", drainTimeout, activePodCount))
			return 30 * time.Second
		}
		log.Info("machine has active runner pods, waiting for drain", "machine", m.Name, "activePods", activePodCount)
		conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonDraining, fmt.Sprintf("Waiting for %d runner pods to finish", activePodCount))
		return 5 * time.Second
	}

	// 3.3 Cordon 後の Quiescence window (スケジューラの in-flight bind 完了待機)
	if drainStartedAt != nil {
		quiescenceWindow := 10 * time.Second
		drainElapsed := time.Since(drainStartedAt.Time)
		if drainElapsed < quiescenceWindow {
			log.Info("waiting for cordon quiescence window before shutting down", "machine", m.Name, "elapsed", drainElapsed)
			return 5 * time.Second
		}
	}

	// 3.4 30秒以内の重複GracefulShutdownを抑止
	if m.Status.Operation != nil && m.Status.Operation.Type == ghav1alpha1.PowerOperationTypeGracefulShutdown {
		if time.Since(m.Status.Operation.LastAttemptAt.Time) < 30*time.Second {
			return 10 * time.Second
		}
	}

	// 3.5 GracefulShutdownの発行
	r.initiateGracefulShutdown(ctx, m, pwrCtrl)
	return 10 * time.Second
}

func (r *RunnerMachineReconciler) handleShutdownTimeout(
	ctx context.Context,
	m *ghav1alpha1.RunnerMachine,
	pwrCtrl redfish.PowerController,
	shutdownTimeout time.Duration,
	timeoutPolicy ghav1alpha1.RedfishTimeoutPolicy,
) {
	log := logf.FromContext(ctx)
	var startedAt time.Time
	if m.Status.Operation != nil && !m.Status.Operation.StartedAt.IsZero() {
		startedAt = m.Status.Operation.StartedAt.Time
	} else if m.Status.LastPowerTransitionTime != nil {
		startedAt = m.Status.LastPowerTransitionTime.Time
	} else {
		return
	}

	elapsed := time.Since(startedAt)
	if elapsed <= shutdownTimeout {
		return
	}
	if r.Recorder != nil {
		r.Recorder.Eventf(m, nil, corev1.EventTypeWarning, "ShutdownStalled", "PowerOff", "Graceful shutdown on machine %s exceeded timeout %s", m.Name, shutdownTimeout)
	}
	conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypePowerReady, metav1.ConditionFalse, conditions.ReasonShutdownStalled, "Graceful shutdown exceeded shutdown timeout")

	// ForceOff は Drain完了が永続記録（DrainVerifiedAt != nil）されている場合のみ許可 (安全原則)
	op := m.Status.Operation
	if op == nil || op.DrainVerifiedAt == nil {
		log.Info("blocking force off because Drain completion was not verified and recorded", "machine", m.Name)
		return
	}

	switch timeoutPolicy {
	case ghav1alpha1.RedfishTimeoutPolicyForceOff:
		// 30秒以内の重複ForceOffを抑止
		if op.Type == ghav1alpha1.PowerOperationTypeForceOff && time.Since(op.LastAttemptAt.Time) < 30*time.Second {
			return
		}

		log.Info("force off timeout policy triggered and drain was verified, initiating hard power cut", "machine", m.Name, "drainVerifiedAt", op.DrainVerifiedAt)
		if r.Recorder != nil {
			r.Recorder.Eventf(m, nil, corev1.EventTypeWarning, "ForceOff", "PowerOff", "Force off timeout policy triggered with verified drain, initiating hard power cut on machine %s", m.Name)
		}
		nowOp := metav1.Now()
		op.Type = ghav1alpha1.PowerOperationTypeForceOff
		op.LastAttemptAt = nowOp
		op.Attempts++

		if pwrCtrl != nil {
			if err := pwrCtrl.ForceOff(ctx); err != nil {
				log.Error(err, "failed to force off machine", "machine", m.Name)
			} else {
				metrics.PowerTransitionsTotal.WithLabelValues(m.Namespace, m.Name, "ForceOff").Inc(ctx)
			}
		}
	case ghav1alpha1.RedfishTimeoutPolicyAbort:
		log.Info("graceful shutdown timed out, abort policy applied; clearing operation", "machine", m.Name)
		m.Status.Operation = nil
	}
}

func (r *RunnerMachineReconciler) initiateGracefulShutdown(
	ctx context.Context,
	m *ghav1alpha1.RunnerMachine,
	pwrCtrl redfish.PowerController,
) {
	log := logf.FromContext(ctx)
	log.Info("initiating graceful shutdown on machine", "machine", m.Name)
	if r.Recorder != nil {
		r.Recorder.Eventf(m, nil, corev1.EventTypeNormal, "GracefulShutdown", "PowerOff", "Initiating graceful shutdown on machine %s", m.Name)
	}

	nowOp := metav1.Now()
	m.Status.Operation = &ghav1alpha1.PowerOperationStatus{
		Type:            ghav1alpha1.PowerOperationTypeGracefulShutdown,
		StartedAt:       nowOp,
		LastAttemptAt:   nowOp,
		DrainVerifiedAt: &nowOp,
		Attempts:        1,
	}

	if pwrCtrl != nil {
		if err := pwrCtrl.GracefulShutdown(ctx); err != nil {
			log.Error(err, "failed to gracefully shutdown machine", "machine", m.Name)
			if r.Recorder != nil {
				r.Recorder.Eventf(m, nil, corev1.EventTypeWarning, "GracefulShutdownFailed", "PowerOff", "Failed to gracefully shutdown machine %s: %v", m.Name, err)
			}
			m.Status.Operation = nil
		} else {
			m.Status.PowerState = ghav1alpha1.PowerStatePoweringOff
			nowTrans := metav1.Now()
			m.Status.LastPowerTransitionTime = &nowTrans
			metrics.PowerTransitionsTotal.WithLabelValues(m.Namespace, m.Name, "PowerOff").Inc(ctx)
		}
	}
}

func (r *RunnerMachineReconciler) countActiveRunnerPodsOnNode(ctx context.Context, cluster *ghav1alpha1.RunnerCluster, nodeName string) (int, error) {
	if !cluster.Status.APIReachable {
		return 0, nil
	}
	remoteClient, err := r.RemoteProvider.GetClient(ctx, cluster)
	if err != nil {
		return 0, fmt.Errorf("failed to get client for remote cluster: %w", err)
	}

	runnerNs := cluster.Spec.RunnerNamespace
	if runnerNs == "" {
		runnerNs = runner.DefaultRunnerNamespace
	}

	var podList corev1.PodList
	selector := labels.SelectorFromSet(map[string]string{
		runner.LabelManagedBy: runner.LabelManagedByValue,
	})
	if err := remoteClient.List(ctx, &podList, client.InNamespace(runnerNs), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return 0, fmt.Errorf("failed to list runner pods on remote cluster: %w", err)
	}

	count := 0
	for _, p := range podList.Items {
		if p.Spec.NodeName == nodeName && p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			count++
		}
	}
	return count, nil
}

func (r *RunnerMachineReconciler) getDesiredState(ctx context.Context, m *ghav1alpha1.RunnerMachine) (ghav1alpha1.MachineDesiredState, *metav1.Time, *ghav1alpha1.RunnerNodePool) {
	// 1. NodePoolRef から desiredState を取得
	if m.Spec.NodePoolRef != nil && m.Spec.NodePoolRef.Name != "" {
		var pool ghav1alpha1.RunnerNodePool
		if err := r.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Spec.NodePoolRef.Name}, &pool); err == nil {
			for _, dm := range pool.Status.DesiredMachines {
				if dm.Name == m.Name || (dm.UID != "" && dm.UID == string(m.UID)) {
					return dm.DesiredState, dm.DrainStartedAt, &pool
				}
			}
			return ghav1alpha1.MachineDesiredStateOff, nil, &pool
		}
	}

	// どのPoolにも属していない場合はOff
	return ghav1alpha1.MachineDesiredStateOff, nil, nil
}

func (r *RunnerMachineReconciler) getRedfishController(ctx context.Context, m *ghav1alpha1.RunnerMachine) (redfish.PowerController, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Spec.Redfish.CredentialsSecretRef.Name}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get redfish secret: %w", err)
	}

	username := string(secret.Data["username"])
	password := string(secret.Data["password"])

	var caCert []byte
	if m.Spec.Redfish.TLS.CASecretRef != nil {
		var caSecret corev1.Secret
		if err := r.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Spec.Redfish.TLS.CASecretRef.Name}, &caSecret); err != nil {
			return nil, fmt.Errorf("failed to get redfish CA secret: %w", err)
		}
		caCert = caSecret.Data[m.Spec.Redfish.TLS.CASecretRef.Key]
	}

	return r.RedfishFactory.NewController(m.Spec.Redfish, username, password, caCert)
}

func (r *RunnerMachineReconciler) recordRedfishSuccess(m *ghav1alpha1.RunnerMachine, at time.Time) {
	if m.Status.RedfishHealth == nil {
		m.Status.RedfishHealth = &ghav1alpha1.RedfishHealthStatus{}
	}
	now := metav1.NewTime(at)
	m.Status.RedfishHealth.LastSuccessTime = &now
	m.Status.RedfishHealth.ConsecutiveFailures = 0
	m.Status.RedfishHealth.Circuit = ghav1alpha1.RedfishCircuitClosed
	m.Status.RedfishHealth.NextProbeTime = nil
	conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeRedfishReachable, metav1.ConditionTrue, conditions.ReasonSuccess, "Redfish communication successful")
}

func (r *RunnerMachineReconciler) recordRedfishFailure(m *ghav1alpha1.RunnerMachine, at time.Time, err error) {
	if m.Status.RedfishHealth == nil {
		m.Status.RedfishHealth = &ghav1alpha1.RedfishHealthStatus{}
	}
	now := metav1.NewTime(at)
	m.Status.RedfishHealth.LastFailureTime = &now
	m.Status.RedfishHealth.ConsecutiveFailures++

	failures := m.Status.RedfishHealth.ConsecutiveFailures
	conditions.SetConditionWithGeneration(&m.Status.Conditions, m.Generation, conditions.TypeRedfishReachable, metav1.ConditionFalse, conditions.ReasonFailed, fmt.Sprintf("Redfish failure count %d: %v", failures, err))

	// サーキットブレーカー: 5回連続失敗でOpenに遷移し指数バックオフ (cap 10分)
	if failures >= 5 {
		m.Status.RedfishHealth.Circuit = ghav1alpha1.RedfishCircuitOpen
		backoff := min(30*time.Second*time.Duration(1<<(failures-5)), 10*time.Minute)
		nextProbe := metav1.NewTime(at.Add(backoff))
		m.Status.RedfishHealth.NextProbeTime = &nextProbe
	}
}

func (r *RunnerMachineReconciler) updateStatus(ctx context.Context, m, _ *ghav1alpha1.RunnerMachine) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var current ghav1alpha1.RunnerMachine
		if err := r.Get(ctx, client.ObjectKeyFromObject(m), &current); err != nil {
			return err
		}
		current.Status = m.Status
		return r.Status().Update(ctx, &current)
	})
}

func (r *RunnerMachineReconciler) findMachinesForNodePool(ctx context.Context, obj client.Object) []ctrl.Request {
	pool, ok := obj.(*ghav1alpha1.RunnerNodePool)
	if !ok {
		return nil
	}

	var machines ghav1alpha1.RunnerMachineList
	if err := listWithIndexFallback(ctx, r.Client, &machines, pool.Namespace, IndexMachineNodePoolRefName, pool.Name); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, m := range machines.Items {
		if m.Spec.NodePoolRef != nil && m.Spec.NodePoolRef.Name == pool.Name {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&m),
			})
		}
	}
	return requests
}

func (r *RunnerMachineReconciler) findMachinesForCluster(ctx context.Context, obj client.Object) []ctrl.Request {
	cluster, ok := obj.(*ghav1alpha1.RunnerCluster)
	if !ok {
		return nil
	}

	var machines ghav1alpha1.RunnerMachineList
	if err := listWithIndexFallback(ctx, r.Client, &machines, cluster.Namespace, IndexClusterRefName, cluster.Name); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, m := range machines.Items {
		if m.Spec.ClusterRef.Name == cluster.Name {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&m),
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *RunnerMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ghav1alpha1.RunnerMachine{}).
		Watches(&ghav1alpha1.RunnerNodePool{}, handler.EnqueueRequestsFromMapFunc(r.findMachinesForNodePool)).
		Watches(&ghav1alpha1.RunnerCluster{}, handler.EnqueueRequestsFromMapFunc(r.findMachinesForCluster)).
		Complete(r)
}
