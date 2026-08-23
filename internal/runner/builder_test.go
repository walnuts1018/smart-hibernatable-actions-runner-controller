package runner

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

func TestBuildJitSecret(t *testing.T) {
	runner := &ghav1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-runner",
			UID:  "runner-uid-123",
		},
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			RunnerName: "test-runner",
			ScaleSetRef: corev1.LocalObjectReference{
				Name: "test-scaleset",
			},
		},
	}

	secret := BuildJitSecret("test-ns", runner, "encoded-jit-token")
	if secret.Name != "test-runner-jit" {
		t.Errorf("expected secret name %q, got %q", "test-runner-jit", secret.Name)
	}

	if secret.StringData[JitConfigSecretKey] != "encoded-jit-token" {
		t.Errorf("expected token in StringData, got %v", secret.StringData[JitConfigSecretKey])
	}
}

func TestBuildRunnerPod(t *testing.T) {
	scaleSet := &ghav1alpha1.RunnerScaleSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-scaleset",
			UID:  "scaleset-uid-123",
		},
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			Runner: ghav1alpha1.RunnerTemplateSpec{
				ContainerName: "runner",
				WorkDir:       "_work",
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "runner",
								Image: "ghcr.io/actions/actions-runner:latest",
							},
						},
					},
				},
			},
		},
	}

	epRunner := &ghav1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-runner",
			UID:  "runner-uid-123",
		},
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			RunnerName: "test-runner",
		},
	}

	pod := BuildRunnerPod("gha-runners", scaleSet, epRunner)
	if pod.Name != "test-runner" {
		t.Errorf("expected pod name %q, got %q", "test-runner", pod.Name)
	}

	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("expected restartPolicy Never, got %v", pod.Spec.RestartPolicy)
	}

	if pod.Labels[LabelManagedBy] != LabelManagedByValue {
		t.Errorf("expected managed-by label %q, got %q", LabelManagedByValue, pod.Labels[LabelManagedBy])
	}

	foundEnv := false
	for _, env := range pod.Spec.Containers[0].Env {
		if env.Name == EnvJitConfig {
			foundEnv = true
			if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil || env.ValueFrom.SecretKeyRef.Name != "test-runner-jit" {
				t.Errorf("unexpected secretKeyRef for JIT env: %+v", env.ValueFrom)
			}
		}
	}

	if !foundEnv {
		t.Errorf("expected %s env var in runner container", EnvJitConfig)
	}
}
