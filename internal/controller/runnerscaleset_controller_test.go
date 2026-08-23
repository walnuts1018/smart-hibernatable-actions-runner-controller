package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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

func TestRunnerScaleSetReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	secret := &corev1.Secret{
		Name:      "github-app-secret",
		Namespace: "default",
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0fake\n-----END RSA PRIVATE KEY-----"),
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name:      "pool1",
		Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			MachineSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"pool": "pool1"},
			},
		},
	}

	machine := &ghav1alpha1.RunnerMachine{
		Name:      "m1",
		Namespace: "default",
		Labels:    map[string]string{"pool": "pool1"},
		Spec: ghav1alpha1.RunnerMachineSpec{
			Capacity: ghav1alpha1.RunnerMachineCapacity{Runners: 4},
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name:       "test-ss",
		Namespace:  "default",
		Finalizers: []string{runner.FinalizerScaleSetCleanup},
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				ConfigURL:            "https://github.com/example-org",
				ScaleSetName:         "test-ss",
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "github-app-secret"},
			},
			NodePoolRef: corev1.LocalObjectReference{Name: "pool1"},
			Scaling: ghav1alpha1.RunnerScaleSetScalingSpec{
				MinRunners: 0,
				MaxRunners: 2,
			},
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
		Status: ghav1alpha1.RunnerScaleSetStatus{
			DesiredRunners: 2,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret, nodePool, machine, scaleSet).WithStatusSubresource(scaleSet).Build()

	r := &RunnerScaleSetReconciler{
		Client: fakeClient,
		Scheme: scheme,
		ScaleSetFactory: &fakeScaleSetFactory{
			fakeClient: &fakeScaleSetClient{scaleSetID: 999},
		},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "test-ss",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Listener Deploymentが作成されているか確認
	var deploy appsv1.Deployment
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss-listener"}, &deploy); err != nil {
		t.Fatalf("expected listener deployment to exist: %v", err)
	}
	if deploy.Spec.Strategy.Type != appsv1.RollingUpdateDeploymentStrategyType {
		t.Fatalf("expected strategy RollingUpdate, got %s", deploy.Spec.Strategy.Type)
	}
	if deploy.Spec.Strategy.RollingUpdate == nil || deploy.Spec.Strategy.RollingUpdate.MaxSurge.IntValue() != 0 {
		t.Fatalf("expected maxSurge 0, got %v", deploy.Spec.Strategy.RollingUpdate)
	}
	if deploy.Spec.Template.Annotations[runner.AnnotationCredentialsHash] == "" {
		t.Fatalf("expected credentials hash annotation to be set")
	}
	if deploy.Spec.Template.Spec.ServiceAccountName != "test-ss-listener" {
		t.Fatalf("expected ServiceAccountName test-ss-listener, got %s", deploy.Spec.Template.Spec.ServiceAccountName)
	}
	if len(deploy.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(deploy.Spec.Template.Spec.Containers))
	}
	c := deploy.Spec.Template.Spec.Containers[0]
	if c.LivenessProbe == nil || c.ReadinessProbe == nil {
		t.Fatal("expected liveness and readiness probes to be configured")
	}

	// ServiceAccount, Role, RoleBinding, Leaseが作成されているか確認
	var sa corev1.ServiceAccount
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss-listener"}, &sa); err != nil {
		t.Fatalf("expected listener service account to exist: %v", err)
	}

	// EphemeralRunnerが2つ作成されているか確認
	var runners ghav1alpha1.EphemeralRunnerList
	if err := fakeClient.List(context.Background(), &runners, client.InNamespace("default")); err != nil {
		t.Fatalf("failed to list runners: %v", err)
	}

	if len(runners.Items) != 2 {
		t.Fatalf("expected 2 EphemeralRunners, got %d", len(runners.Items))
	}
}

func TestRunnerScaleSetReconciler_DeletionWithSecretMissingAndOrphanOverride(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	now := metav1.Now()
	// Case 1: Secretが欠落しており、orphan overrideがない場合 -> Finalizerは維持されて削除ブロック
	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name:              "test-ss",
		Namespace:         "default",
		Finalizers:        []string{runner.FinalizerScaleSetCleanup},
		DeletionTimestamp: &now,
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				ConfigURL:            "https://github.com/example-org",
				ScaleSetName:         "test-ss",
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "missing-secret"},
			},
			NodePoolRef: corev1.LocalObjectReference{Name: "pool1"},
		},
		Status: ghav1alpha1.RunnerScaleSetStatus{
			ScaleSetID: 999,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(scaleSet).WithStatusSubresource(scaleSet).Build()

	r := &RunnerScaleSetReconciler{
		Client: fakeClient,
		Scheme: scheme,
		ScaleSetFactory: &fakeScaleSetFactory{
			fakeClient: &fakeScaleSetClient{scaleSetID: 999},
		},
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "test-ss",
	})
	if err != nil {
		t.Fatalf("unexpected error during deletion reconciliation: %v", err)
	}
	if res.RequeueAfter != 30*time.Second {
		t.Errorf("expected 30s requeue when secret is missing, got %v", res.RequeueAfter)
	}

	var updated ghav1alpha1.RunnerScaleSet
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &updated); err != nil {
		t.Fatalf("failed to get scale set: %v", err)
	}
	if len(updated.Finalizers) == 0 {
		t.Errorf("expected finalizers to be retained when secret is missing without orphan override")
	}

	// Case 2: orphan-github-resource アノテーションが付与された場合 -> Finalizerが解除される
	updated.Annotations = map[string]string{
		runner.AnnotationOrphanGitHubResource: "true",
	}
	fakeClient.Update(context.Background(), &updated)

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "test-ss",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &updated)
	if err == nil && len(updated.Finalizers) != 0 {
		t.Errorf("expected finalizer to be removed with orphan override, got %v", updated.Finalizers)
	}
}
