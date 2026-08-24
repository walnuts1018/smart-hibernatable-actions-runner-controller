package runner

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

func TestBuildJitSecret(t *testing.T) {
	runner := &ghav1alpha1.EphemeralRunner{
		Name: "test-runner",
		UID:  "runner-uid-123",
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
		Name: "test-scaleset",
		UID:  "scaleset-uid-123",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			Runner: ghav1alpha1.RunnerTemplateSpec{
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
		Name: "test-runner",
		UID:  "runner-uid-123",
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

func TestBuildRunnerPod_MetricsInjection(t *testing.T) {
	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "test-scaleset",
		UID:  "scaleset-uid-123",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			Metrics: &ghav1alpha1.MetricsSpec{
				Enabled:  true,
				Endpoint: "http://default-collector.opentelemetry-collector.svc.cluster.local:4318/v1/metrics",
				ExtraAttributes: map[string]string{
					"cluster": "test-cluster",
				},
			},
			Runner: ghav1alpha1.RunnerTemplateSpec{
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
		Name: "test-runner",
		UID:  "runner-uid-123",
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			RunnerName: "test-runner",
		},
	}

	pod := BuildRunnerPod("gha-runners", scaleSet, epRunner)

	// 1. Verify initContainer
	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("expected 1 initContainer, got %d", len(pod.Spec.InitContainers))
	}
	initC := pod.Spec.InitContainers[0]
	if initC.Name != RunnerHookInitContainerName {
		t.Errorf("expected initContainer name %q, got %q", RunnerHookInitContainerName, initC.Name)
	}

	// 2. Verify volumes
	volMap := make(map[string]corev1.Volume)
	for _, v := range pod.Spec.Volumes {
		volMap[v.Name] = v
	}
	if _, ok := volMap[RunnerHookVolumeName]; !ok {
		t.Errorf("missing volume %s", RunnerHookVolumeName)
	}
	if _, ok := volMap[CgroupRootVolumeName]; !ok {
		t.Errorf("missing volume %s", CgroupRootVolumeName)
	}

	// 3. Verify container volume mounts and env vars
	c := pod.Spec.Containers[0]
	mountMap := make(map[string]corev1.VolumeMount)
	for _, vm := range c.VolumeMounts {
		mountMap[vm.Name] = vm
	}
	if _, ok := mountMap[RunnerHookVolumeName]; !ok {
		t.Errorf("missing volume mount %s", RunnerHookVolumeName)
	}
	if _, ok := mountMap[CgroupRootVolumeName]; !ok {
		t.Errorf("missing volume mount %s", CgroupRootVolumeName)
	}

	envMap := make(map[string]corev1.EnvVar)
	for _, env := range c.Env {
		envMap[env.Name] = env
	}

	if env, ok := envMap[EnvActionsRunnerHookJobStarted]; !ok || env.Value != RunnerHookMountPath+"/job-started" {
		t.Errorf("expected %s=%s, got %v", EnvActionsRunnerHookJobStarted, RunnerHookMountPath+"/job-started", env)
	}
	if env, ok := envMap[EnvActionsRunnerHookJobCompleted]; !ok || env.Value != RunnerHookMountPath+"/job-completed" {
		t.Errorf("expected %s=%s, got %v", EnvActionsRunnerHookJobCompleted, RunnerHookMountPath+"/job-completed", env)
	}
	if env, ok := envMap[EnvRunnerMetricsEndpoint]; !ok || env.Value != "http://default-collector.opentelemetry-collector.svc.cluster.local:4318/v1/metrics" {
		t.Errorf("expected %s to match endpoint, got %v", EnvRunnerMetricsEndpoint, env)
	}
	if _, ok := envMap[EnvPodUID]; !ok {
		t.Errorf("expected %s env var", EnvPodUID)
	}
	if _, ok := envMap[EnvRunnerMetricsExtraAttrs]; !ok {
		t.Errorf("expected %s env var", EnvRunnerMetricsExtraAttrs)
	}
}
