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
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/runner"
)

func TestRunnerMachineReconciler_ScaleFromZero_PowerOnAndUncordon(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default"},
		Status: ghav1alpha1.RunnerClusterStatus{
			APIReachable: true,
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "redfish-secret", Namespace: "default"},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("password"),
		},
	}

	machine := &ghav1alpha1.RunnerMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "m1",
			Namespace: "default",
			UID:       "machine-uid-1",
			Labels:    map[string]string{"pool": "p1"},
		},
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:         corev1.LocalObjectReference{Name: "c1"},
			KubernetesNodeName: "node1",
			Capacity:           ghav1alpha1.RunnerMachineCapacity{Runners: 2},
			Bootstrap:          true,
			Redfish: ghav1alpha1.RedfishSpec{
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "redfish-secret"},
			},
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOff,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
			MachineSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": "p1"},
			},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			DesiredMachines: []ghav1alpha1.MachinePlanStatus{
				{
					Name:         "m1",
					UID:          "machine-uid-1",
					DesiredState: ghav1alpha1.MachineDesiredStateActive,
				},
			},
		},
	}

	remoteNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Annotations: map[string]string{
				runner.AnnotationCordonedBy: "machine-uid-1",
			},
		},
		Spec: corev1.NodeSpec{
			Unschedulable: true,
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, secret, machine, nodePool).
		WithStatusSubresource(machine, nodePool).
		Build()

	remoteClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(remoteNode).Build()
	remoteProvider := &fakeRemoteProvider{client: remoteClient, node: remoteNode}

	pwrCtrl := &fakePowerController{powerState: ghav1alpha1.PowerStateOff}
	pwrFactory := &fakePowerControllerFactory{fakeCtrl: pwrCtrl}

	r := &RunnerMachineReconciler{
		Client:         fakeClient,
		Scheme:         scheme,
		RemoteProvider: remoteProvider,
		RedfishFactory: pwrFactory,
	}

	// 1. First reconcile: 電源OFF状態からPowerOnが呼ばれる
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "m1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !pwrCtrl.powerOnCalled {
		t.Errorf("expected PowerOn to be called")
	}

	// 2. Second reconcile: 電源ONになりNodeがReadyになったらUncordonされる
	pwrCtrl.powerState = ghav1alpha1.PowerStateOn
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "m1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updatedNode corev1.Node
	if err := remoteClient.Get(context.Background(), client.ObjectKey{Name: "node1"}, &updatedNode); err != nil {
		t.Fatalf("failed to get node: %v", err)
	}

	if updatedNode.Spec.Unschedulable {
		t.Errorf("expected node to be uncordoned (Unschedulable=false)")
	}
	if _, exists := updatedNode.Annotations[runner.AnnotationCordonedBy]; exists {
		t.Errorf("expected cordoned-by annotation to be removed")
	}
}

func TestRunnerMachineReconciler_ScaleDown_DrainingAndShutdown(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			APIReachable: true,
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "redfish-secret", Namespace: "default"},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("password"),
		},
	}

	machine := &ghav1alpha1.RunnerMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "m1",
			Namespace: "default",
			UID:       "machine-uid-1",
			Labels:    map[string]string{"pool": "p1"},
		},
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:         corev1.LocalObjectReference{Name: "c1"},
			KubernetesNodeName: "node1",
			Capacity:           ghav1alpha1.RunnerMachineCapacity{Runners: 2},
			Bootstrap:          true,
			Redfish: ghav1alpha1.RedfishSpec{
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "redfish-secret"},
			},
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOn,
		},
	}

	drainPast := metav1.NewTime(time.Now().Add(-15 * time.Minute))
	nodePool := &ghav1alpha1.RunnerNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
			MachineSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": "p1"},
			},
			Scaling: ghav1alpha1.RunnerNodePoolScalingSpec{
				ScaleDownDelay: &metav1.Duration{Duration: 10 * time.Minute},
			},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			DesiredMachines: []ghav1alpha1.MachinePlanStatus{
				{
					Name:           "m1",
					UID:            "machine-uid-1",
					DesiredState:   ghav1alpha1.MachineDesiredStateOff,
					DrainStartedAt: &drainPast,
				},
			},
		},
	}

	remoteNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
		},
		Spec: corev1.NodeSpec{
			Unschedulable: false,
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	// 実行中のRunner Podが存在する状態
	runnerPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runner-1",
			Namespace: "gha-runners",
			Labels: map[string]string{
				runner.LabelManagedBy: runner.LabelManagedByValue,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "node1",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, secret, machine, nodePool).
		WithStatusSubresource(machine, nodePool).
		Build()

	remoteClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(remoteNode, runnerPod).Build()
	remoteProvider := &fakeRemoteProvider{client: remoteClient, node: remoteNode}

	pwrCtrl := &fakePowerController{powerState: ghav1alpha1.PowerStateOn}
	pwrFactory := &fakePowerControllerFactory{fakeCtrl: pwrCtrl}

	r := &RunnerMachineReconciler{
		Client:         fakeClient,
		Scheme:         scheme,
		RemoteProvider: remoteProvider,
		RedfishFactory: pwrFactory,
	}

	// 1. Pod実行中の場合: NodeがCordonされるが、GracefulShutdownは呼ばれない（Drain待ち）
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "m1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pwrCtrl.shutdownCalled {
		t.Errorf("expected GracefulShutdown NOT to be called while active runner pods exist")
	}

	var updatedNode corev1.Node
	if err := remoteClient.Get(context.Background(), client.ObjectKey{Name: "node1"}, &updatedNode); err != nil {
		t.Fatalf("failed to get node: %v", err)
	}
	if !updatedNode.Spec.Unschedulable {
		t.Errorf("expected node to be cordoned during scale down")
	}

	// 2. Runner Podが完了（削除）された場合: GracefulShutdownが実行される
	_ = remoteClient.Delete(context.Background(), runnerPod)

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "m1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !pwrCtrl.shutdownCalled {
		t.Errorf("expected GracefulShutdown to be called after runner pod finished and ScaleDownDelay elapsed")
	}
}

func TestRunnerMachineReconciler_ExternalCordonProtection(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default"},
		Status: ghav1alpha1.RunnerClusterStatus{
			APIReachable: true,
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "redfish-secret", Namespace: "default"},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("password"),
		},
	}

	machine := &ghav1alpha1.RunnerMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "m1",
			Namespace: "default",
			UID:       "machine-uid-1",
			Labels:    map[string]string{"pool": "p1"},
		},
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:         corev1.LocalObjectReference{Name: "c1"},
			KubernetesNodeName: "node1",
			Capacity:           ghav1alpha1.RunnerMachineCapacity{Runners: 2},
			Bootstrap:          true,
			Redfish: ghav1alpha1.RedfishSpec{
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "redfish-secret"},
			},
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOn,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
			MachineSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": "p1"},
			},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			DesiredMachines: []ghav1alpha1.MachinePlanStatus{
				{
					Name:         "m1",
					UID:          "machine-uid-1",
					DesiredState: ghav1alpha1.MachineDesiredStateOff,
				},
			},
		},
	}

	// 管理者が手動でkubectl cordonしたノード (Unschedulable=true, cordoned-by annotationなし)
	externalCordonedNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
		},
		Spec: corev1.NodeSpec{
			Unschedulable: true,
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, secret, machine, nodePool).
		WithStatusSubresource(machine, nodePool).
		Build()

	remoteClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(externalCordonedNode).Build()
	remoteProvider := &fakeRemoteProvider{client: remoteClient, node: externalCordonedNode}

	pwrCtrl := &fakePowerController{powerState: ghav1alpha1.PowerStateOn}
	pwrFactory := &fakePowerControllerFactory{fakeCtrl: pwrCtrl}

	r := &RunnerMachineReconciler{
		Client:         fakeClient,
		Scheme:         scheme,
		RemoteProvider: remoteProvider,
		RedfishFactory: pwrFactory,
	}

	// 1. スケールダウンReconcile: 外部Cordonノードに対してSHARCが所有権を奪わず、電源OFFもブロックされること
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "m1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pwrCtrl.shutdownCalled {
		t.Errorf("expected GracefulShutdown NOT to be called on externally cordoned node")
	}

	var nodeCheck corev1.Node
	if err := remoteClient.Get(context.Background(), client.ObjectKey{Name: "node1"}, &nodeCheck); err != nil {
		t.Fatalf("failed to get node: %v", err)
	}

	// SHARCの所有権アノテーションが付与されていないことを検証
	if _, exists := nodeCheck.Annotations[runner.AnnotationCordonedBy]; exists {
		t.Errorf("expected cordoned-by annotation NOT to be added to externally cordoned node")
	}

	var machineCheck ghav1alpha1.RunnerMachine
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "m1"}, &machineCheck); err != nil {
		t.Fatalf("failed to get machine: %v", err)
	}
	if !machineCheck.Status.ExternallyCordoned {
		t.Errorf("expected ExternallyCordoned to be true")
	}

	// 2. Activeに戻った場合でも勝手にUncordonしないことを検証
	nodePool.Status.DesiredMachines[0].DesiredState = ghav1alpha1.MachineDesiredStateActive
	_ = fakeClient.Status().Update(context.Background(), nodePool)

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "m1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := remoteClient.Get(context.Background(), client.ObjectKey{Name: "node1"}, &nodeCheck); err != nil {
		t.Fatalf("failed to get node: %v", err)
	}
	if !nodeCheck.Spec.Unschedulable {
		t.Errorf("expected externally cordoned node to remain Unschedulable=true")
	}
}
