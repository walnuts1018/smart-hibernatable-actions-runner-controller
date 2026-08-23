package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
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
	Recorder       record.EventRecorder
	RemoteProvider remotecluster.RemoteClusterProvider
	RedfishFactory redfish.PowerControllerFactory
}

// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnermachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnermachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnermachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnernodepools,verbs=get;list;watch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnerclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *RunnerMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var machine ghav1alpha1.RunnerMachine
	if err := r.Get(ctx, req.NamespacedName, &machine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	origMachine := machine.DeepCopy()

	// 1. 所属するRunnerClusterを取得
	var cluster ghav1alpha1.RunnerCluster
	if err := r.Get(ctx, client.ObjectKey{Namespace: machine.Namespace, Name: machine.Spec.ClusterRef.Name}, &cluster); err != nil {
		log.Error(err, "failed to get cluster for machine", "cluster", machine.Spec.ClusterRef.Name)
		conditions.SetCondition(&machine.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, fmt.Sprintf("Cluster %s not found: %v", machine.Spec.ClusterRef.Name, err))
		_ = r.updateStatus(ctx, &machine, origMachine)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// 2. 所属するRunnerNodePoolからDesiredStateとDrain開始時刻を取得
	desiredState, drainStartedAt, nodePool, err := r.getDesiredState(ctx, &machine)
	if err != nil {
		log.Error(err, "failed to get desired state from node pool")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// 3. Redfish BMCから最新の電源状態を観測
	pwrCtrl, err := r.getRedfishController(ctx, &machine)
	if err != nil {
		log.Error(err, "failed to create redfish controller")
		conditions.SetCondition(&machine.Status.Conditions, conditions.TypeRedfishReachable, metav1.ConditionFalse, conditions.ReasonSecretNotFound, err.Error())
	} else {
		state, err := pwrCtrl.GetPowerState(ctx)
		if err != nil {
			log.Error(err, "failed to get power state from redfish")
			conditions.SetCondition(&machine.Status.Conditions, conditions.TypeRedfishReachable, metav1.ConditionFalse, conditions.ReasonUnsupportedRedfish, err.Error())
		} else {
			conditions.SetCondition(&machine.Status.Conditions, conditions.TypeRedfishReachable, metav1.ConditionTrue, conditions.ReasonSuccess, "Redfish is reachable")
			if machine.Status.PowerState != state {
				now := metav1.Now()
				machine.Status.LastPowerTransitionTime = &now
				machine.Status.PowerState = state
			}

			// 電源状態が目標と一致したらOperationをクリア
			if machine.Status.Operation != nil {
				if machine.Status.PowerState == ghav1alpha1.PowerStateOn && machine.Status.Operation.Type == ghav1alpha1.PowerOperationTypePowerOn {
					machine.Status.Operation = nil
				} else if machine.Status.PowerState == ghav1alpha1.PowerStateOff && (machine.Status.Operation.Type == ghav1alpha1.PowerOperationTypeGracefulShutdown || machine.Status.Operation.Type == ghav1alpha1.PowerOperationTypeForceOff) {
					machine.Status.Operation = nil
				}
			}
		}
	}

	// 4. リモートKubernetesクラスタのNode状態を観測
	var remoteNode *corev1.Node
	if cluster.Status.APIReachable {
		node, err := r.RemoteProvider.GetNode(ctx, &cluster, machine.Spec.KubernetesNodeName)
		if err != nil || node == nil {
			machine.Status.Kubernetes.Present = false
			machine.Status.Kubernetes.Ready = false
			conditions.SetCondition(&machine.Status.Conditions, conditions.TypeKubernetesNodeReady, metav1.ConditionFalse, conditions.ReasonNodeNotFound, "Kubernetes node not found")
		} else {
			remoteNode = node
			machine.Status.Kubernetes.Present = true
			machine.Status.Kubernetes.Ready = remotecluster.IsNodeReady(node)
			machine.Status.Kubernetes.NodeUID = string(node.UID)
			currentMachineID := node.Status.NodeInfo.MachineID
			if currentMachineID != "" {
				if machine.Status.Kubernetes.MachineID != "" && machine.Status.Kubernetes.MachineID != currentMachineID {
					log.Error(nil, "node machineID mismatch, possible host collision", "machine", machine.Name, "expected", machine.Status.Kubernetes.MachineID, "actual", currentMachineID)
					if r.Recorder != nil {
						r.Recorder.Eventf(&machine, corev1.EventTypeWarning, "MachineIDMismatch", "Node %s has different machineID %s (expected %s)", node.Name, currentMachineID, machine.Status.Kubernetes.MachineID)
					}
				}
				machine.Status.Kubernetes.MachineID = currentMachineID
			}

			if machine.Status.Kubernetes.Ready {
				conditions.SetCondition(&machine.Status.Conditions, conditions.TypeKubernetesNodeReady, metav1.ConditionTrue, conditions.ReasonNodeReady, "Kubernetes node is Ready")
			} else {
				conditions.SetCondition(&machine.Status.Conditions, conditions.TypeKubernetesNodeReady, metav1.ConditionFalse, conditions.ReasonNodeNotReady, "Kubernetes node is NotReady")
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
						conditions.SetCondition(&machine.Status.Conditions, conditions.TypeQuarantined, metav1.ConditionTrue, conditions.ReasonQuarantined, fmt.Sprintf("Node failed to become Ready within %s", nodeReadyTimeout))
						if r.Recorder != nil {
							r.Recorder.Eventf(&machine, corev1.EventTypeWarning, "MachineQuarantined", "Machine %s quarantined: Node failed to become Ready within %s", machine.Name, nodeReadyTimeout)
						}
					}
				}
			}
		}
	} else {
		machine.Status.Kubernetes.Present = false
		machine.Status.Kubernetes.Ready = false
		conditions.SetCondition(&machine.Status.Conditions, conditions.TypeKubernetesNodeReady, metav1.ConditionFalse, conditions.ReasonAPIUnreachable, "Cluster API unreachable")
	}

	// 5. DesiredStateに応じた電源操作・Cordon制御
	requeueAfter := 30 * time.Second
	switch desiredState {
	case ghav1alpha1.MachineDesiredStateActive:
		requeueAfter = r.reconcileActive(ctx, &machine, &cluster, nodePool, remoteNode, pwrCtrl)
	case ghav1alpha1.MachineDesiredStateOff:
		requeueAfter = r.reconcileOff(ctx, &machine, &cluster, nodePool, remoteNode, pwrCtrl, drainStartedAt)
	}

	// 6. Overall Ready Condition の設定
	if machine.Status.PowerState == ghav1alpha1.PowerStateOn && machine.Status.Kubernetes.Ready {
		conditions.SetCondition(&machine.Status.Conditions, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonReady, "Machine is powered on and Node is Ready")
	} else if machine.Status.PowerState == ghav1alpha1.PowerStateOff && desiredState == ghav1alpha1.MachineDesiredStateOff {
		conditions.SetCondition(&machine.Status.Conditions, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonScaledToZero, "Machine is powered off (scaled to zero)")
	} else {
		conditions.SetCondition(&machine.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "Machine is not fully ready")
	}

	// メトリクス更新
	stateVal := 0.0
	if machine.Status.PowerState == ghav1alpha1.PowerStateOn {
		stateVal = 1.0
	}
	metrics.MachinePowerState.WithLabelValues(machine.Namespace, machine.Name, string(machine.Status.PowerState)).Set(stateVal)

	// Status更新
	if err := r.updateStatus(ctx, &machine, origMachine); err != nil {
		log.Error(err, "failed to patch runner machine status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
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

func (r *RunnerMachineReconciler) reconcileActive(
	ctx context.Context,
	m *ghav1alpha1.RunnerMachine,
	cluster *ghav1alpha1.RunnerCluster,
	nodePool *ghav1alpha1.RunnerNodePool,
	remoteNode *corev1.Node,
	pwrCtrl redfish.PowerController,
) time.Duration {
	log := logf.FromContext(ctx)

	// メンテナンスモードの場合は電源操作を行わない
	if m.Spec.Maintenance != nil && m.Spec.Maintenance.Enabled {
		log.Info("machine is in maintenance mode, skipping power management", "machine", m.Name)
		conditions.SetCondition(&m.Status.Conditions, conditions.TypeMaintenance, metav1.ConditionTrue, conditions.ReasonMaintenance, "Machine is under maintenance")
		return 1 * time.Minute
	}

	// 1. Shutdown Commit Point以降の方向転換保護: シャットダウン処理中の場合はまずOffになるのを待つ
	if m.Status.Operation != nil && (m.Status.Operation.Type == ghav1alpha1.PowerOperationTypeGracefulShutdown || m.Status.Operation.Type == ghav1alpha1.PowerOperationTypeForceOff) {
		if m.Status.PowerState != ghav1alpha1.PowerStateOff {
			log.Info("waiting for in-flight shutdown to complete before restarting machine", "machine", m.Name, "operation", m.Status.Operation.Type)
			return 5 * time.Second
		}
		// OffになったらOperationをクリアしてPowerOnへ進む
		m.Status.Operation = nil
	}

	// 2. もし電源OFFならPowerOnを実行
	if m.Status.PowerState == ghav1alpha1.PowerStateOff {
		if m.Status.Operation != nil && m.Status.Operation.Type == ghav1alpha1.PowerOperationTypePowerOn {
			if time.Since(m.Status.Operation.LastAttemptAt.Time) < 30*time.Second {
				log.Info("skipping duplicate PowerOn command within debounce interval", "machine", m.Name)
				return 10 * time.Second
			}
		}

		log.Info("powering on machine", "machine", m.Name)
		if r.Recorder != nil {
			r.Recorder.Eventf(m, corev1.EventTypeNormal, "PoweringOn", "Powering on machine %s", m.Name)
		}

		now := metav1.Now()
		attempts := int32(1)
		if m.Status.Operation != nil && m.Status.Operation.Type == ghav1alpha1.PowerOperationTypePowerOn {
			attempts = m.Status.Operation.Attempts + 1
		}
		m.Status.Operation = &ghav1alpha1.PowerOperationStatus{
			Type:          ghav1alpha1.PowerOperationTypePowerOn,
			StartedAt:     now,
			LastAttemptAt: now,
			Attempts:      attempts,
		}

		if pwrCtrl != nil {
			if err := pwrCtrl.PowerOn(ctx); err != nil {
				log.Error(err, "failed to power on machine", "machine", m.Name)
				if r.Recorder != nil {
					r.Recorder.Eventf(m, corev1.EventTypeWarning, "PowerOnFailed", "Failed to power on machine %s: %v", m.Name, err)
				}
			} else {
				metrics.PowerTransitionsTotal.WithLabelValues(m.Namespace, m.Name, "PowerOn").Inc()
			}
		}
		return 10 * time.Second
	}

	// 3. 起動中なら待機
	if m.Status.PowerState == ghav1alpha1.PowerStatePoweringOn {
		return 10 * time.Second
	}

	// 4. 電源ONかつNodeがReadyなら、自身が設定したCordonのみ解除（外部Cordonは保護）
	if m.Status.PowerState == ghav1alpha1.PowerStateOn && remoteNode != nil && m.Status.Kubernetes.Ready {
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

	// 5. Quarantine安定化判定: Nodeが2分間継続してReadyならQuarantine解除
	if m.Status.Quarantine != nil {
		if m.Status.Kubernetes.Ready {
			now := metav1.Now()
			if m.Status.Quarantine.HealthySince == nil {
				m.Status.Quarantine.HealthySince = &now
			} else if time.Since(m.Status.Quarantine.HealthySince.Time) >= 2*time.Minute {
				log.Info("clearing quarantine after stabilization period", "machine", m.Name)
				m.Status.Quarantine = nil
				conditions.SetCondition(&m.Status.Conditions, conditions.TypeQuarantined, metav1.ConditionFalse, conditions.ReasonReady, "Machine is stable and healthy")
			}
		} else {
			m.Status.Quarantine.HealthySince = nil
		}
	}

	return 30 * time.Second
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

	// メンテナンスモードの場合は電源OFF操作を行わない
	if m.Spec.Maintenance != nil && m.Spec.Maintenance.Enabled {
		log.Info("machine is in maintenance mode, skipping power down", "machine", m.Name)
		conditions.SetCondition(&m.Status.Conditions, conditions.TypeMaintenance, metav1.ConditionTrue, conditions.ReasonMaintenance, "Machine is under maintenance")
		return 1 * time.Minute
	}

	// 1. 既に電源OFF
	if m.Status.PowerState == ghav1alpha1.PowerStateOff {
		return 1 * time.Minute
	}

	shutdownTimeout := 3 * time.Minute
	if m.Spec.Redfish.Power.ShutdownTimeout != nil && m.Spec.Redfish.Power.ShutdownTimeout.Duration > 0 {
		shutdownTimeout = m.Spec.Redfish.Power.ShutdownTimeout.Duration
	}

	var forceOffAfter time.Duration
	if m.Spec.Redfish.Power.ForceOffAfter != nil {
		forceOffAfter = m.Spec.Redfish.Power.ForceOffAfter.Duration
	}

	// 2. シャットダウン中 (PoweringOff) のタイムアウト・ForceOff判定
	if m.Status.PowerState == ghav1alpha1.PowerStatePoweringOff {
		if m.Status.LastPowerTransitionTime != nil {
			elapsed := time.Since(m.Status.LastPowerTransitionTime.Time)
			if elapsed > shutdownTimeout {
				if r.Recorder != nil {
					r.Recorder.Eventf(m, corev1.EventTypeWarning, "ShutdownStalled", "Graceful shutdown on machine %s exceeded timeout %s", m.Name, shutdownTimeout)
				}
				conditions.SetCondition(&m.Status.Conditions, conditions.TypePowerReady, metav1.ConditionFalse, conditions.ReasonShutdownStalled, "Graceful shutdown exceeded shutdown timeout")
				if forceOffAfter > 0 && elapsed >= (shutdownTimeout+forceOffAfter) {
					log.Info("force off timeout reached, initiating hard power cut", "machine", m.Name)
					if r.Recorder != nil {
						r.Recorder.Eventf(m, corev1.EventTypeWarning, "ForceOff", "Force off timeout reached, initiating hard power cut on machine %s", m.Name)
					}
					if pwrCtrl != nil {
						if err := pwrCtrl.ForceOff(ctx); err != nil {
							log.Error(err, "failed to force off machine", "machine", m.Name)
						} else {
							metrics.PowerTransitionsTotal.WithLabelValues(m.Namespace, m.Name, "ForceOff").Inc()
						}
					}
				}
			}
		}
		return 10 * time.Second
	}

	// 3. 電源ONの場合: Drainingおよび安全なスケールダウン判定
	if m.Status.PowerState == ghav1alpha1.PowerStateOn {
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
					conditions.SetCondition(&m.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonExternalCordon, "Node is cordoned externally; scale down power off is blocked")
					return 30 * time.Second
				}
				m.Status.ExternallyCordoned = false
			}
		}

		// 3.2 このNode上で現在実行中のRunner Podが存在するか確認
		activePodCount := r.countActiveRunnerPodsOnNode(ctx, cluster, m.Spec.KubernetesNodeName)
		if activePodCount > 0 {
			log.Info("machine has active runner pods, waiting for drain", "machine", m.Name, "activePods", activePodCount)
			conditions.SetCondition(&m.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonDraining, fmt.Sprintf("Waiting for %d runner pods to finish", activePodCount))
			return 5 * time.Second
		}

		// 3.3 ScaleDownDelayの確認
		scaleDownDelay := 10 * time.Minute
		if nodePool != nil && nodePool.Spec.Scaling.ScaleDownDelay != nil && nodePool.Spec.Scaling.ScaleDownDelay.Duration > 0 {
			scaleDownDelay = nodePool.Spec.Scaling.ScaleDownDelay.Duration
		}

		if drainStartedAt != nil {
			drainElapsed := time.Since(drainStartedAt.Time)
			if drainElapsed < scaleDownDelay {
				remaining := scaleDownDelay - drainElapsed
				log.Info("machine scale-down delayed by ScaleDownDelay", "machine", m.Name, "remaining", remaining)
				return 10 * time.Second
			}
		}

		// 3.4 30秒以内の重複GracefulShutdownを抑止
		if m.Status.Operation != nil && m.Status.Operation.Type == ghav1alpha1.PowerOperationTypeGracefulShutdown {
			if time.Since(m.Status.Operation.LastAttemptAt.Time) < 30*time.Second {
				return 10 * time.Second
			}
		}

		// 3.5 GracefulShutdownの発行
		log.Info("initiating graceful shutdown on machine", "machine", m.Name)
		if r.Recorder != nil {
			r.Recorder.Eventf(m, corev1.EventTypeNormal, "GracefulShutdown", "Initiating graceful shutdown on machine %s", m.Name)
		}

		nowOp := metav1.Now()
		m.Status.Operation = &ghav1alpha1.PowerOperationStatus{
			Type:          ghav1alpha1.PowerOperationTypeGracefulShutdown,
			StartedAt:     nowOp,
			LastAttemptAt: nowOp,
			Attempts:      1,
		}

		if pwrCtrl != nil {
			if err := pwrCtrl.GracefulShutdown(ctx); err != nil {
				log.Error(err, "failed to gracefully shutdown machine", "machine", m.Name)
				if r.Recorder != nil {
					r.Recorder.Eventf(m, corev1.EventTypeWarning, "GracefulShutdownFailed", "Failed to gracefully shutdown machine %s: %v", m.Name, err)
				}
			} else {
				m.Status.PowerState = ghav1alpha1.PowerStatePoweringOff
				nowTrans := metav1.Now()
				m.Status.LastPowerTransitionTime = &nowTrans
				metrics.PowerTransitionsTotal.WithLabelValues(m.Namespace, m.Name, "PowerOff").Inc()
			}
		}
		return 10 * time.Second
	}

	return 30 * time.Second
}

func (r *RunnerMachineReconciler) countActiveRunnerPodsOnNode(ctx context.Context, cluster *ghav1alpha1.RunnerCluster, nodeName string) int {
	if !cluster.Status.APIReachable {
		return 0
	}
	remoteClient, err := r.RemoteProvider.GetClient(ctx, cluster)
	if err != nil {
		return 0
	}

	runnerNs := cluster.Spec.RunnerNamespace
	if runnerNs == "" {
		runnerNs = "gha-runners"
	}

	var podList corev1.PodList
	selector := labels.SelectorFromSet(map[string]string{
		runner.LabelManagedBy: runner.LabelManagedByValue,
	})
	if err := remoteClient.List(ctx, &podList, client.InNamespace(runnerNs), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return 0
	}

	count := 0
	for _, p := range podList.Items {
		if p.Spec.NodeName == nodeName && p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			count++
		}
	}
	return count
}

func (r *RunnerMachineReconciler) getDesiredState(ctx context.Context, m *ghav1alpha1.RunnerMachine) (ghav1alpha1.MachineDesiredState, *metav1.Time, *ghav1alpha1.RunnerNodePool, error) {
	var pools ghav1alpha1.RunnerNodePoolList
	if err := r.List(ctx, &pools, client.InNamespace(m.Namespace)); err != nil {
		return ghav1alpha1.MachineDesiredStateOff, nil, nil, err
	}

	for _, p := range pools.Items {
		selector, err := metav1.LabelSelectorAsSelector(&p.Spec.MachineSelector)
		if err == nil && selector.Matches(labels.Set(m.Labels)) {
			// 所属するNodePoolが見つかった
			for _, dm := range p.Status.DesiredMachines {
				if dm.Name == m.Name || (dm.UID != "" && dm.UID == string(m.UID)) {
					return dm.DesiredState, dm.DrainStartedAt, &p, nil
				}
			}
			return ghav1alpha1.MachineDesiredStateOff, nil, &p, nil
		}
	}

	// どのPoolにも属していない場合はOff
	return ghav1alpha1.MachineDesiredStateOff, nil, nil, nil
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
		if err := r.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Spec.Redfish.TLS.CASecretRef.Name}, &caSecret); err == nil {
			caKey := m.Spec.Redfish.TLS.CASecretRef.Key
			if caKey == "" {
				caKey = "ca.crt"
			}
			caCert = caSecret.Data[caKey]
		}
	}

	return r.RedfishFactory.NewController(m.Spec.Redfish, username, password, caCert)
}

func (r *RunnerMachineReconciler) updateStatus(ctx context.Context, m, orig *ghav1alpha1.RunnerMachine) error {
	return r.Status().Patch(ctx, m, client.MergeFrom(orig))
}

func (r *RunnerMachineReconciler) findMachinesForNodePool(ctx context.Context, obj client.Object) []ctrl.Request {
	pool, ok := obj.(*ghav1alpha1.RunnerNodePool)
	if !ok {
		return nil
	}

	selector, err := metav1.LabelSelectorAsSelector(&pool.Spec.MachineSelector)
	if err != nil {
		return nil
	}

	var machineList ghav1alpha1.RunnerMachineList
	if err := r.List(ctx, &machineList, client.InNamespace(pool.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, m := range machineList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: client.ObjectKey{
				Namespace: m.Namespace,
				Name:      m.Name,
			},
		})
	}
	return requests
}

func (r *RunnerMachineReconciler) findMachinesForCluster(ctx context.Context, obj client.Object) []ctrl.Request {
	cluster, ok := obj.(*ghav1alpha1.RunnerCluster)
	if !ok {
		return nil
	}

	var machineList ghav1alpha1.RunnerMachineList
	if err := r.List(ctx, &machineList, client.InNamespace(cluster.Namespace), client.MatchingFields{
		IndexClusterRefName: cluster.Name,
	}); err != nil {
		if err := r.List(ctx, &machineList, client.InNamespace(cluster.Namespace)); err != nil {
			return nil
		}
		var requests []ctrl.Request
		for _, m := range machineList.Items {
			if m.Spec.ClusterRef.Name == cluster.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Namespace: m.Namespace,
						Name:      m.Name,
					},
				})
			}
		}
		return requests
	}

	var requests []ctrl.Request
	for _, m := range machineList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: client.ObjectKey{
				Namespace: m.Namespace,
				Name:      m.Name,
			},
		})
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
