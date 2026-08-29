package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

func TestIndexesAndFallback(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	// Test fake client with configured field indexer
	builder := setupFakeClientBuilder(scheme)

	m1 := &ghav1alpha1.RunnerMachine{
		Namespace: "default",
		Name:      "m1",
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:  corev1.LocalObjectReference{Name: "c1"},
			NodePoolRef: &corev1.LocalObjectReference{Name: "p1"},
		},
	}
	m2 := &ghav1alpha1.RunnerMachine{
		Namespace: "default",
		Name:      "m2",
		Spec: ghav1alpha1.RunnerMachineSpec{
			ClusterRef:  corev1.LocalObjectReference{Name: "c2"},
			NodePoolRef: &corev1.LocalObjectReference{Name: "p2"},
		},
	}

	cl := builder.WithObjects(m1, m2).Build()

	// 1. Test listWithIndexFallback using valid index
	var list ghav1alpha1.RunnerMachineList
	err := listWithIndexFallback(context.Background(), cl, &list, "default", IndexClusterRefName, "c1")
	if err != nil {
		t.Fatalf("unexpected error from listWithIndexFallback: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Name != "m1" {
		t.Errorf("expected 1 machine 'm1', got %d items", len(list.Items))
	}

	// 2. Test listWithIndexFallback using non-indexed or fallback field
	var fallbackList ghav1alpha1.RunnerMachineList
	err = listWithIndexFallback(context.Background(), cl, &fallbackList, "default", ".non.existent.field", "c1")
	if err != nil {
		t.Fatalf("unexpected error from fallback list: %v", err)
	}
	if len(fallbackList.Items) != 2 {
		t.Errorf("expected all 2 items on fallback, got %d", len(fallbackList.Items))
	}
}

func TestIndexFunctions(t *testing.T) {
	// 1. RunnerScaleSet -> NodePoolRef
	ss := &ghav1alpha1.RunnerScaleSet{
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "pool-a"},
		},
	}
	if ss.Spec.NodePoolRef.Name != "pool-a" {
		t.Errorf("expected pool-a")
	}

	// 2. EphemeralRunner -> GitHub RunnerName
	er1 := &ghav1alpha1.EphemeralRunner{
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Provisioning: &ghav1alpha1.ProvisioningAttemptStatus{
				RunnerName: "runner-prov-1",
			},
		},
	}
	if er1.Status.Provisioning.RunnerName != "runner-prov-1" {
		t.Errorf("expected runner-prov-1")
	}

	er2 := &ghav1alpha1.EphemeralRunner{
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			RunnerName: "runner-spec-1",
		},
	}
	if er2.Spec.RunnerName != "runner-spec-1" {
		t.Errorf("expected runner-spec-1")
	}
}
