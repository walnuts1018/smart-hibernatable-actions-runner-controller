package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

func TestRunnerClusterReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name:      "test-cluster",
		Namespace: "default",
		Spec: ghav1alpha1.RunnerClusterSpec{
			KubeconfigSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "test-kubeconfig"},
				Key:                  "kubeconfig",
			},
		},
	}

	machine := &ghav1alpha1.RunnerMachine{
		Name:      "m1",
		Namespace: "default",
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:         corev1.LocalObjectReference{Name: "test-cluster"},
			NodeName: "node1",
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOn,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, machine).WithStatusSubresource(cluster, machine).Build()
	remoteProvider := &fakeRemoteProvider{healthErr: nil}

	r := &RunnerClusterReconciler{
		Client:         fakeClient,
		Scheme:         scheme,
		RemoteProvider: remoteProvider,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "test-cluster",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated ghav1alpha1.RunnerCluster
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-cluster"}, &updated); err != nil {
		t.Fatalf("failed to get updated cluster: %v", err)
	}

	if updated.Status.Phase != ghav1alpha1.RunnerClusterPhaseReady {
		t.Errorf("expected phase %s, got %s", ghav1alpha1.RunnerClusterPhaseReady, updated.Status.Phase)
	}
	if !updated.Status.APIReachable {
		t.Errorf("expected APIReachable true")
	}
}

func TestRunnerClusterReconciler_ShortCircuitOffline(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name:      "test-cluster",
		Namespace: "default",
	}

	machine := &ghav1alpha1.RunnerMachine{
		Name:      "m1",
		Namespace: "default",
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "test-cluster"},
		},
		Status: ghav1alpha1.RunnerMachineStatus{
			PowerState: ghav1alpha1.PowerStateOff,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, machine).WithStatusSubresource(cluster, machine).Build()
	// healthErrがある場合でも、ショートサーキットによりCheckHealthが呼ばれずOfflineになること
	remoteProvider := &fakeRemoteProvider{healthErr: context.DeadlineExceeded}

	r := &RunnerClusterReconciler{
		Client:         fakeClient,
		Scheme:         scheme,
		RemoteProvider: remoteProvider,
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "test-cluster",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated ghav1alpha1.RunnerCluster
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-cluster"}, &updated); err != nil {
		t.Fatalf("failed to get updated cluster: %v", err)
	}

	if updated.Status.Phase != ghav1alpha1.RunnerClusterPhaseOffline {
		t.Errorf("expected phase %s, got %s", ghav1alpha1.RunnerClusterPhaseOffline, updated.Status.Phase)
	}
	if updated.Status.APIReachable {
		t.Errorf("expected APIReachable false")
	}
}

func TestRunnerClusterReconciler_Phases(t *testing.T) {
	tests := []struct {
		name          string
		machineState  ghav1alpha1.PowerState
		healthErr     error
		expectedPhase ghav1alpha1.RunnerClusterPhase
	}{
		{
			name:          "Offline when all machines are Off and API unreachable",
			machineState:  ghav1alpha1.PowerStateOff,
			healthErr:     context.DeadlineExceeded,
			expectedPhase: ghav1alpha1.RunnerClusterPhaseOffline,
		},
		{
			name:          "Starting when machine is PoweringOn and API unreachable",
			machineState:  ghav1alpha1.PowerStatePoweringOn,
			healthErr:     context.DeadlineExceeded,
			expectedPhase: ghav1alpha1.RunnerClusterPhaseStarting,
		},
		{
			name:          "Degraded when machine is On but API unreachable",
			machineState:  ghav1alpha1.PowerStateOn,
			healthErr:     context.DeadlineExceeded,
			expectedPhase: ghav1alpha1.RunnerClusterPhaseDegraded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			clientgoscheme.AddToScheme(scheme)
			ghav1alpha1.AddToScheme(scheme)

			cluster := &ghav1alpha1.RunnerCluster{
				Name: "c1", Namespace: "default",
			}
			machine := &ghav1alpha1.RunnerMachine{
				Name: "m1", Namespace: "default",
				Spec: ghav1alpha1.RunnerMachineSpec{
					ClusterRef: corev1.LocalObjectReference{Name: "c1"},
				},
				Status: ghav1alpha1.RunnerMachineStatus{
					PowerState: tt.machineState,
				},
			}

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, machine).WithStatusSubresource(cluster, machine).Build()
			remoteProvider := &fakeRemoteProvider{healthErr: tt.healthErr}

			r := &RunnerClusterReconciler{
				Client:         fakeClient,
				Scheme:         scheme,
				RemoteProvider: remoteProvider,
			}

			_, err := r.Reconcile(context.Background(), ctrl.Request{
				Namespace: "default", Name: "c1",
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var updated ghav1alpha1.RunnerCluster
			if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "c1"}, &updated); err != nil {
				t.Fatalf("failed to get updated cluster: %v", err)
			}

			if updated.Status.Phase != tt.expectedPhase {
				t.Errorf("expected phase %s, got %s", tt.expectedPhase, updated.Status.Phase)
			}
		})
	}
}
