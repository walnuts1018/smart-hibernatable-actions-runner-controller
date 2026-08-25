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

func TestEphemeralRunnerSetReconciler_ScaleUpAndDown(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	two := int32(2)
	ers := &ghav1alpha1.EphemeralRunnerSet{
		Name:      "test-ss",
		Namespace: "default",
		Spec: ghav1alpha1.EphemeralRunnerSetSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "test-ss"},
			Replicas:    &two,
			Runner: ghav1alpha1.RunnerTemplateSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "runner", Image: "runner:latest"},
						},
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ers).WithStatusSubresource(ers).Build()

	r := &EphemeralRunnerSetReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// 1. Scale Up: replicas=2 -> 2 EphemeralRunners created
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "test-ss",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var runners ghav1alpha1.EphemeralRunnerList
	if err := fakeClient.List(context.Background(), &runners, client.InNamespace("default")); err != nil {
		t.Fatalf("failed to list runners: %v", err)
	}

	if len(runners.Items) != 2 {
		t.Fatalf("expected 2 EphemeralRunners created, got %d", len(runners.Items))
	}

	// 2. Scale Down: replicas=1 -> 1 runner deleted
	var updatedERS ghav1alpha1.EphemeralRunnerSet
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &updatedERS); err != nil {
		t.Fatalf("failed to get ERS: %v", err)
	}
	one := int32(1)
	updatedERS.Spec.Replicas = &one
	if err := fakeClient.Update(context.Background(), &updatedERS); err != nil {
		t.Fatalf("failed to update ERS: %v", err)
	}

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "test-ss",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := fakeClient.List(context.Background(), &runners, client.InNamespace("default")); err != nil {
		t.Fatalf("failed to list runners: %v", err)
	}

	if len(runners.Items) != 1 {
		t.Fatalf("expected 1 EphemeralRunner after scale-down, got %d", len(runners.Items))
	}
}

func TestEphemeralRunnerSetReconciler_ScaleDownWithBusyRunner(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	zero := int32(0)
	ers := &ghav1alpha1.EphemeralRunnerSet{
		Name:      "test-ss",
		Namespace: "default",
		Spec: ghav1alpha1.EphemeralRunnerSetSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "test-ss"},
			Replicas:    &zero,
			Runner: ghav1alpha1.RunnerTemplateSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "runner", Image: "runner:latest"},
						},
					},
				},
			},
		},
	}

	// Busy 状態の Runner (ジョブ実行中のためスケールダウンで即座に削除されない)
	busyRunner := &ghav1alpha1.EphemeralRunner{
		Name:      "test-ss-busy",
		Namespace: "default",
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "test-ss"},
			RunnerName:  "test-ss-busy",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseBusy,
		},
	}

	// Starting 状態の Runner (ジョブ未実行のためスケールダウンで削除される)
	startingRunner := &ghav1alpha1.EphemeralRunner{
		Name:      "test-ss-starting",
		Namespace: "default",
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "test-ss"},
			RunnerName:  "test-ss-starting",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseStarting,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(ers, busyRunner, startingRunner).
		WithStatusSubresource(ers, busyRunner, startingRunner).
		WithIndex(&ghav1alpha1.EphemeralRunner{}, IndexScaleSetRefName, func(obj client.Object) []string {
			er, ok := obj.(*ghav1alpha1.EphemeralRunner)
			if !ok || er.Spec.ScaleSetRef.Name == "" {
				return nil
			}
			return []string{er.Spec.ScaleSetRef.Name}
		}).
		Build()

	r := &EphemeralRunnerSetReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// 1. Reconcile: startingRunner は削除されるが busyRunner は保持され、RequeueAfter: 5s が返る
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "test-ss",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Errorf("expected RequeueAfter to be set for unresolved excess, got 0")
	}

	var runners ghav1alpha1.EphemeralRunnerList
	if err := fakeClient.List(context.Background(), &runners, client.InNamespace("default")); err != nil {
		t.Fatalf("failed to list runners: %v", err)
	}
	if len(runners.Items) != 1 {
		t.Fatalf("expected exactly 1 runner (busyRunner) still present, got %d", len(runners.Items))
	}
	if runners.Items[0].Name != "test-ss-busy" {
		t.Errorf("expected test-ss-busy to remain, got %s", runners.Items[0].Name)
	}
}
