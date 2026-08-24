package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
			"github_app_private_key":     []byte(validTestRSAPrivateKeyPEM),
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name:      "pool1",
		Namespace: "default",
		Status: ghav1alpha1.RunnerNodePoolStatus{
			PotentialRunnerCapacity: 4,
			ReadyRunnerCapacity:     4,
		},
	}

	two := int32(2)
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
				MaxRunners: &two,
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
	}

	fakeClient := setupFakeClientBuilder(scheme).
		WithObjects(secret, nodePool, scaleSet).
		WithStatusSubresource(scaleSet, nodePool).
		Build()

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

	// ServiceAccountが作成されているか確認
	var sa corev1.ServiceAccount
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss-listener"}, &sa); err != nil {
		t.Fatalf("expected listener service account to exist: %v", err)
	}

	// EphemeralRunnerSetが作成されているか確認
	var ers ghav1alpha1.EphemeralRunnerSet
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &ers); err != nil {
		t.Fatalf("expected EphemeralRunnerSet to exist: %v", err)
	}
	if ers.Spec.ScaleSetRef.Name != "test-ss" {
		t.Fatalf("expected scaleSetRef.name to be test-ss, got %s", ers.Spec.ScaleSetRef.Name)
	}
}

func TestRunnerScaleSetReconciler_Deletion(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	now := metav1.Now()
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

	fakeClient := setupFakeClientBuilder(scheme).
		WithObjects(scaleSet).
		WithStatusSubresource(scaleSet).
		Build()

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
		t.Fatalf("unexpected error during deletion reconciliation: %v", err)
	}

	var updated ghav1alpha1.RunnerScaleSet
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &updated)
	if err == nil && len(updated.Finalizers) != 0 {
		t.Errorf("expected finalizers to be removed, got %v", updated.Finalizers)
	}
}

func TestRunnerScaleSetReconciler_ListenerCustomization(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	secret := &corev1.Secret{
		Name:      "github-app-secret",
		Namespace: "default",
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte(validTestRSAPrivateKeyPEM),
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name:      "pool1",
		Namespace: "default",
		Status: ghav1alpha1.RunnerNodePoolStatus{
			PotentialRunnerCapacity: 4,
			ReadyRunnerCapacity:     4,
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name:       "custom-listener-ss",
		Namespace:  "default",
		Finalizers: []string{runner.FinalizerScaleSetCleanup},
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				ConfigURL:            "https://github.com/example-org",
				ScaleSetName:         "custom-listener-ss",
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "github-app-secret"},
			},
			NodePoolRef: corev1.LocalObjectReference{Name: "pool1"},
			Listener: ghav1alpha1.ListenerSpec{
				Image:              "custom-listener:v1",
				ServiceAccountName: "custom-sa",
				NodeSelector: map[string]string{
					"kubernetes.io/os": "linux",
				},
				Tolerations: []corev1.Toleration{
					{
						Key:      "dedicated",
						Operator: corev1.TolerationOpEqual,
						Value:    "ci",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				Labels: map[string]string{
					"custom-label": "custom-value",
				},
				Annotations: map[string]string{
					"custom-annotation": "custom-value",
				},
				Env: []corev1.EnvVar{
					{Name: "EXTRA_ENV", Value: "extra_val"},
				},
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
	}

	fakeClient := setupFakeClientBuilder(scheme).
		WithObjects(secret, nodePool, scaleSet).
		WithStatusSubresource(scaleSet, nodePool).
		Build()

	r := &RunnerScaleSetReconciler{
		Client: fakeClient,
		Scheme: scheme,
		ScaleSetFactory: &fakeScaleSetFactory{
			fakeClient: &fakeScaleSetClient{scaleSetID: 999},
		},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "custom-listener-ss",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var deploy appsv1.Deployment
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "custom-listener-ss-listener"}, &deploy); err != nil {
		t.Fatalf("expected listener deployment to exist: %v", err)
	}

	if deploy.Spec.Template.Spec.ServiceAccountName != "custom-sa" {
		t.Errorf("expected ServiceAccountName custom-sa, got %s", deploy.Spec.Template.Spec.ServiceAccountName)
	}
	if deploy.Spec.Template.Spec.NodeSelector["kubernetes.io/os"] != "linux" {
		t.Errorf("expected nodeSelector linux, got %v", deploy.Spec.Template.Spec.NodeSelector)
	}
	if len(deploy.Spec.Template.Spec.Tolerations) != 1 || deploy.Spec.Template.Spec.Tolerations[0].Key != "dedicated" {
		t.Errorf("expected toleration with key dedicated, got %v", deploy.Spec.Template.Spec.Tolerations)
	}
	if deploy.Spec.Template.Labels["custom-label"] != "custom-value" {
		t.Errorf("expected custom-label in pod template labels, got %v", deploy.Spec.Template.Labels)
	}
	if deploy.Spec.Template.Annotations["custom-annotation"] != "custom-value" {
		t.Errorf("expected custom-annotation in pod template annotations, got %v", deploy.Spec.Template.Annotations)
	}
	if len(deploy.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(deploy.Spec.Template.Spec.Containers))
	}
	c := deploy.Spec.Template.Spec.Containers[0]
	if c.Image != "custom-listener:v1" {
		t.Errorf("expected container image custom-listener:v1, got %s", c.Image)
	}
	foundExtraEnv := false
	for _, env := range c.Env {
		if env.Name == "EXTRA_ENV" && env.Value == "extra_val" {
			foundExtraEnv = true
			break
		}
	}
	if !foundExtraEnv {
		t.Errorf("expected EXTRA_ENV=extra_val in container env, got %v", c.Env)
	}
}
