package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/capacity"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/conditions"
)

func TestRunnerNodePoolReconciler_DesiredMachinesPlanning(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
		Spec: ghav1alpha1.RunnerClusterSpec{
			Startup: &ghav1alpha1.RunnerClusterStartupSpec{
				MachineRefs: []corev1.LocalObjectReference{
					{Name: "m1"},
				},
			},
		},
	}

	machine1 := &ghav1alpha1.RunnerMachine{
		Name:      "m1",
		Namespace: "default",
		UID:       "uid-m1",
		Labels:    map[string]string{"pool": "p1"},
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:  corev1.LocalObjectReference{Name: "c1"},
			NodePoolRef: &corev1.LocalObjectReference{Name: "p1"},
			NodeName:    "node1",
			Priority:    100,
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOn,
			Kubernetes: ghav1alpha1.RunnerMachineKubernetesStatus{
				Ready: true,
			},
		},
	}

	machine2 := &ghav1alpha1.RunnerMachine{
		Name:      "m2",
		Namespace: "default",
		UID:       "uid-m2",
		Labels:    map[string]string{"pool": "p1"},
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:  corev1.LocalObjectReference{Name: "c1"},
			NodePoolRef: &corev1.LocalObjectReference{Name: "p1"},
			NodeName:    "node2",
			Priority:    200,
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOff,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
	}

	now := metav1.Now()
	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
		Status: ghav1alpha1.RunnerScaleSetStatus{
			GitHub: ghav1alpha1.GitHubStatisticsStatus{
				AssignedJobs:       2,
				LastStatisticsTime: &now,
			},
		},
	}

	two := int32(2)
	ers := &ghav1alpha1.EphemeralRunnerSet{
		Name:      "ss1",
		Namespace: "default",
		Spec: ghav1alpha1.EphemeralRunnerSetSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			Replicas:    &two,
		},
	}

	fakeClient := setupFakeClientBuilder(scheme).
		WithObjects(cluster, machine1, machine2, nodePool, scaleSet, ers).
		WithStatusSubresource(machine1, machine2, nodePool, scaleSet, ers).
		Build()

	planner := capacity.NewOrderedCapacityPlanner(true) // multi-node enabled
	remoteProvider := &fakeRemoteProvider{}

	r := &RunnerNodePoolReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		RemoteProvider:  remoteProvider,
		Planner:         planner,
		EnableMultiNode: true,
	}

	// 1. Startupであるm1がActive、m2がOffとして計画される
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "p1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updatedPool ghav1alpha1.RunnerNodePool
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "p1"}, &updatedPool); err != nil {
		t.Fatalf("failed to get updated pool: %v", err)
	}

	if updatedPool.Status.DesiredNodes != 1 {
		t.Errorf("expected DesiredNodes=1, got %d", updatedPool.Status.DesiredNodes)
	}

	if len(updatedPool.Status.DesiredMachines) != 2 {
		t.Fatalf("expected 2 machine plan statuses, got %d", len(updatedPool.Status.DesiredMachines))
	}

	planMap := make(map[string]ghav1alpha1.MachinePlanStatus)
	for _, p := range updatedPool.Status.DesiredMachines {
		planMap[p.Name] = p
	}

	if planMap["m1"].DesiredState != ghav1alpha1.MachineDesiredStateActive {
		t.Errorf("expected m1 desiredState to be Active, got %s", planMap["m1"].DesiredState)
	}
	if planMap["m2"].DesiredState != ghav1alpha1.MachineDesiredStateOff {
		t.Errorf("expected m2 desiredState to be Off, got %s", planMap["m2"].DesiredState)
	}
	if planMap["m2"].DrainStartedAt == nil {
		t.Errorf("expected m2 to have DrainStartedAt recorded")
	}

	// 2. 需要が0になった場合（m1はstartup保持、m2はOffとして計画され、IdleSinceが開始される）
	var currentERS ghav1alpha1.EphemeralRunnerSet
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "ss1"}, &currentERS); err != nil {
		t.Fatalf("failed to get current ERS: %v", err)
	}
	zero := int32(0)
	currentERS.Spec.Replicas = &zero
	if err := fakeClient.Update(context.Background(), &currentERS); err != nil {
		t.Fatalf("failed to update ERS: %v", err)
	}

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "p1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "p1"}, &updatedPool); err != nil {
		t.Fatalf("failed to get updated pool: %v", err)
	}

	if updatedPool.Status.IdleSince == nil {
		t.Errorf("expected IdleSince to be set when demand dropped to zero")
	}
}

func TestRunnerNodePoolReconciler_MultiNodeDisabledViolation(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
	}

	machine1 := &ghav1alpha1.RunnerMachine{
		Name:      "m1",
		Namespace: "default",
		Labels:    map[string]string{"pool": "p1"},
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:  corev1.LocalObjectReference{Name: "c1"},
			NodePoolRef: &corev1.LocalObjectReference{Name: "p1"},
			NodeName:    "node1",
		},
	}
	machine2 := &ghav1alpha1.RunnerMachine{
		Name:      "m2",
		Namespace: "default",
		Labels:    map[string]string{"pool": "p1"},
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:  corev1.LocalObjectReference{Name: "c1"},
			NodePoolRef: &corev1.LocalObjectReference{Name: "p1"},
			NodeName:    "node2",
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
	}

	two := int32(2)
	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
			Scaling: ghav1alpha1.RunnerScaleSetScalingSpec{
				MaxRunners: &two,
			},
		},
	}

	ers := &ghav1alpha1.EphemeralRunnerSet{
		Name:      "ss1",
		Namespace: "default",
		Spec: ghav1alpha1.EphemeralRunnerSetSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			Replicas:    &two,
		},
	}

	fakeClient := setupFakeClientBuilder(scheme).
		WithObjects(cluster, machine1, machine2, nodePool, scaleSet, ers).
		WithStatusSubresource(machine1, machine2, nodePool, scaleSet, ers).
		Build()

	planner := capacity.NewOrderedCapacityPlanner(false) // multi-node disabled

	r := &RunnerNodePoolReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		RemoteProvider:  &fakeRemoteProvider{},
		Planner:         planner,
		EnableMultiNode: false,
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "p1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.RequeueAfter != 1*time.Minute {
		t.Errorf("expected 1m requeue on multi-node violation, got %v", res.RequeueAfter)
	}
}

func TestRunnerNodePoolReconciler_SchedulerFeedbackLoop(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
	}

	machine1 := &ghav1alpha1.RunnerMachine{
		Name:      "m1",
		Namespace: "default",
		UID:       "uid-m1",
		Labels:    map[string]string{"pool": "p1"},
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:  corev1.LocalObjectReference{Name: "c1"},
			NodePoolRef: &corev1.LocalObjectReference{Name: "p1"},
			NodeName:    "node1",
			Priority:    200, // higher priority
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOff,
		},
	}

	machine2 := &ghav1alpha1.RunnerMachine{
		Name:      "m2",
		Namespace: "default",
		UID:       "uid-m2",
		Labels:    map[string]string{"pool": "p1"},
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:  corev1.LocalObjectReference{Name: "c1"},
			NodePoolRef: &corev1.LocalObjectReference{Name: "p1"},
			NodeName:    "node2",
			Priority:    100, // lower priority
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOff,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
	}

	// Pending runner pod waiting to be scheduled
	runner1 := &ghav1alpha1.EphemeralRunner{
		Name:      "r1",
		Namespace: "default",
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "r1",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseProvisioning,
			Conditions: []metav1.Condition{
				{
					Type:    "PodScheduled",
					Status:  metav1.ConditionFalse,
					Reason:  "Unschedulable",
					Message: "0/0 nodes are available",
				},
			},
		},
	}

	fakeClient := setupFakeClientBuilder(scheme).
		WithObjects(cluster, machine1, machine2, nodePool, scaleSet, runner1).
		WithStatusSubresource(machine1, machine2, nodePool, scaleSet, runner1).
		Build()

	planner := capacity.NewOrderedCapacityPlanner(true) // multi-node enabled

	r := &RunnerNodePoolReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		RemoteProvider:  &fakeRemoteProvider{},
		Planner:         planner,
		EnableMultiNode: true,
	}

	// Reconcile: should select higher priority machine (m1) to start
	_, err := r.Reconcile(context.Background(), ctrl.Request{Namespace: "default", Name: "p1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updatedPool ghav1alpha1.RunnerNodePool
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "p1"}, &updatedPool); err != nil {
		t.Fatalf("failed to get updated pool: %v", err)
	}

	planMap := make(map[string]ghav1alpha1.MachinePlanStatus)
	for _, p := range updatedPool.Status.DesiredMachines {
		planMap[p.Name] = p
	}

	if planMap["m1"].DesiredState != ghav1alpha1.MachineDesiredStateActive {
		t.Errorf("expected m1 desiredState to be Active, got %s", planMap["m1"].DesiredState)
	}
	if planMap["m2"].DesiredState != ghav1alpha1.MachineDesiredStateOff {
		t.Errorf("expected m2 desiredState to be Off, got %s", planMap["m2"].DesiredState)
	}

	// CapacityReady condition should be False with Reason NodesStarting
	cond := conditions.GetCondition(updatedPool.Status.Conditions, conditions.TypeCapacityReady)
	if cond == nil || cond.Reason != conditions.ReasonNodesStarting {
		t.Errorf("expected CapacityReady reason to be NodesStarting, got %v", cond)
	}

	// Now simulate m1 becomes Ready, but runner1 is still Unschedulable (e.g. requires m2)
	machine1.Status.PowerState = ghav1alpha1.PowerStateOn
	machine1.Status.Kubernetes.Ready = true
	if err := fakeClient.Status().Update(context.Background(), machine1); err != nil {
		t.Fatalf("failed to update m1 status: %v", err)
	}

	// Reconcile again: m1 is already Active and Ready, so now m2 should be selected!
	_, err = r.Reconcile(context.Background(), ctrl.Request{Namespace: "default", Name: "p1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "p1"}, &updatedPool); err != nil {
		t.Fatalf("failed to get updated pool: %v", err)
	}

	planMap = make(map[string]ghav1alpha1.MachinePlanStatus)
	for _, p := range updatedPool.Status.DesiredMachines {
		planMap[p.Name] = p
	}

	if planMap["m1"].DesiredState != ghav1alpha1.MachineDesiredStateActive || planMap["m2"].DesiredState != ghav1alpha1.MachineDesiredStateActive {
		t.Errorf("expected both m1 and m2 to be Active, got m1=%s, m2=%s", planMap["m1"].DesiredState, planMap["m2"].DesiredState)
	}

	// Now simulate m2 also becomes Ready, and runner1 remains unschedulable -> PoolExhausted!
	machine2.Status.PowerState = ghav1alpha1.PowerStateOn
	machine2.Status.Kubernetes.Ready = true
	if err := fakeClient.Status().Update(context.Background(), machine2); err != nil {
		t.Fatalf("failed to update m2 status: %v", err)
	}

	_, err = r.Reconcile(context.Background(), ctrl.Request{Namespace: "default", Name: "p1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "p1"}, &updatedPool); err != nil {
		t.Fatalf("failed to get updated pool: %v", err)
	}

	cond = conditions.GetCondition(updatedPool.Status.Conditions, conditions.TypeCapacityReady)
	if cond == nil || cond.Reason != conditions.ReasonPoolExhausted {
		t.Errorf("expected CapacityReady reason to be PoolExhausted, got %v", cond)
	}
}

func TestRunnerNodePoolReconciler_OpportunisticScaleDown(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
	}

	machine1 := &ghav1alpha1.RunnerMachine{
		Name:      "m1",
		Namespace: "default",
		UID:       "uid-m1",
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:  corev1.LocalObjectReference{Name: "c1"},
			NodePoolRef: &corev1.LocalObjectReference{Name: "p1"},
			NodeName:    "node1",
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOn,
			Kubernetes: ghav1alpha1.RunnerMachineKubernetesStatus{Ready: true},
		},
	}

	machine2 := &ghav1alpha1.RunnerMachine{
		Name:      "m2",
		Namespace: "default",
		UID:       "uid-m2",
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:  corev1.LocalObjectReference{Name: "c1"},
			NodePoolRef: &corev1.LocalObjectReference{Name: "p1"},
			NodeName:    "node2",
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOn,
			Kubernetes: ghav1alpha1.RunnerMachineKubernetesStatus{Ready: true},
		},
	}

	delay := metav1.Duration{Duration: 1 * time.Second}
	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
			Scaling: ghav1alpha1.RunnerNodePoolScalingSpec{
				ScaleDownDelay: &delay,
			},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			DesiredMachines: []ghav1alpha1.MachinePlanStatus{
				{Name: "m1", DesiredState: ghav1alpha1.MachineDesiredStateActive},
				{Name: "m2", DesiredState: ghav1alpha1.MachineDesiredStateActive},
			},
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
	}

	// Runner running on m2 (node2). m1 (node1) has 0 runners.
	runnerOnM2 := &ghav1alpha1.EphemeralRunner{
		Name:      "r2",
		Namespace: "default",
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "r2",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseBusy,
			RemotePod: ghav1alpha1.RemotePodStatus{
				NodeName: "node2",
			},
		},
	}

	fakeClient := setupFakeClientBuilder(scheme).
		WithObjects(cluster, machine1, machine2, nodePool, scaleSet, runnerOnM2).
		WithStatusSubresource(machine1, machine2, nodePool, scaleSet, runnerOnM2).
		Build()

	planner := capacity.NewOrderedCapacityPlanner(true)

	r := &RunnerNodePoolReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		RemoteProvider:  &fakeRemoteProvider{},
		Planner:         planner,
		EnableMultiNode: true,
	}

	// First reconcile: m1 has 0 runners, no unschedulable demand -> starts IdleSince on m1
	_, err := r.Reconcile(context.Background(), ctrl.Request{Namespace: "default", Name: "p1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updatedPool ghav1alpha1.RunnerNodePool
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "p1"}, &updatedPool); err != nil {
		t.Fatalf("failed to get updated pool: %v", err)
	}

	planMap := make(map[string]ghav1alpha1.MachinePlanStatus)
	for _, p := range updatedPool.Status.DesiredMachines {
		planMap[p.Name] = p
	}

	if planMap["m1"].IdleSince == nil {
		t.Errorf("expected m1 to have IdleSince initialized")
	}
	if planMap["m2"].IdleSince != nil {
		t.Errorf("expected m2 (with running runner) to have nil IdleSince")
	}

	// Manually set m1's IdleSince to 2 seconds ago to simulate scaleDownDelay expiry
	twoSecAgo := metav1.NewTime(time.Now().Add(-2 * time.Second))
	for i := range updatedPool.Status.DesiredMachines {
		if updatedPool.Status.DesiredMachines[i].Name == "m1" {
			updatedPool.Status.DesiredMachines[i].IdleSince = &twoSecAgo
		}
	}
	if err := fakeClient.Status().Update(context.Background(), &updatedPool); err != nil {
		t.Fatalf("failed to update pool status: %v", err)
	}

	// Second reconcile: m1 exceeded scaleDownDelay -> DesiredState becomes Off! m2 stays Active.
	_, err = r.Reconcile(context.Background(), ctrl.Request{Namespace: "default", Name: "p1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "p1"}, &updatedPool); err != nil {
		t.Fatalf("failed to get updated pool: %v", err)
	}

	planMap = make(map[string]ghav1alpha1.MachinePlanStatus)
	for _, p := range updatedPool.Status.DesiredMachines {
		planMap[p.Name] = p
	}

	if planMap["m1"].DesiredState != ghav1alpha1.MachineDesiredStateOff {
		t.Errorf("expected m1 desiredState to be Off after idle timeout, got %s", planMap["m1"].DesiredState)
	}
	if planMap["m2"].DesiredState != ghav1alpha1.MachineDesiredStateActive {
		t.Errorf("expected m2 desiredState to remain Active, got %s", planMap["m2"].DesiredState)
	}
}
