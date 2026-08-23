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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/capacity"
)

func TestRunnerNodePoolReconciler_DesiredMachinesPlanning(t *testing.T) {
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
			ClusterRef:         corev1.LocalObjectReference{Name: "c1"},
			KubernetesNodeName: "node1",
			Capacity:           ghav1alpha1.RunnerMachineCapacity{Runners: 2},
			Bootstrap:          true,
			Priority:           100,
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
			ClusterRef:         corev1.LocalObjectReference{Name: "c1"},
			KubernetesNodeName: "node2",
			Capacity:           ghav1alpha1.RunnerMachineCapacity{Runners: 2},
			Bootstrap:          false,
			Priority:           200,
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOn,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
			MachineSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": "p1"},
			},
		},
	}

	now := metav1.Now()
	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
		Status: ghav1alpha1.RunnerScaleSetStatus{
			DesiredRunners: 2,
			GitHub: ghav1alpha1.GitHubStatisticsStatus{
				AssignedJobs:       2,
				LastStatisticsTime: &now,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, machine1, machine2, nodePool, scaleSet).
		WithStatusSubresource(machine1, machine2, nodePool, scaleSet).
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

	// 1. 需要2の場合（Bootstrapであるm1がActive、m2がOffとして計画される）
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

	// 2. 需要が0になった場合（m1もm2もOffとして計画され、IdleSinceが開始される）
	scaleSet.Status.DesiredRunners = 0
	fakeClient.Status().Update(context.Background(), scaleSet)

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "p1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "p1"}, &updatedPool); err != nil {
		t.Fatalf("failed to get updated pool: %v", err)
	}

	if updatedPool.Status.DesiredNodes != 0 {
		t.Errorf("expected DesiredNodes=0, got %d", updatedPool.Status.DesiredNodes)
	}
	if updatedPool.Status.IdleSince == nil {
		t.Errorf("expected IdleSince to be set when demand dropped to zero")
	}

	for _, p := range updatedPool.Status.DesiredMachines {
		if p.DesiredState != ghav1alpha1.MachineDesiredStateOff {
			t.Errorf("expected machine %s to be Off, got %s", p.Name, p.DesiredState)
		}
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
			ClusterRef:         corev1.LocalObjectReference{Name: "c1"},
			KubernetesNodeName: "node1",
			Capacity:           ghav1alpha1.RunnerMachineCapacity{Runners: 2},
			Bootstrap:          true,
		},
	}
	machine2 := &ghav1alpha1.RunnerMachine{
		Name:      "m2",
		Namespace: "default",
		Labels:    map[string]string{"pool": "p1"},
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:         corev1.LocalObjectReference{Name: "c1"},
			KubernetesNodeName: "node2",
			Capacity:           ghav1alpha1.RunnerMachineCapacity{Runners: 2},
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
			MachineSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": "p1"},
			},
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
		Status: ghav1alpha1.RunnerScaleSetStatus{
			DesiredRunners: 2,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, machine1, machine2, nodePool, scaleSet).
		WithStatusSubresource(machine1, machine2, nodePool, scaleSet).
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
