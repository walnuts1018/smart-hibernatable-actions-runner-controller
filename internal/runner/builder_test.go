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

func TestBuildRunnerPod_DinDInjection(t *testing.T) {
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
								Image: "ghcr.io/actions/actions-runner:2.336.0",
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

	// 1. Verify initContainers
	if len(pod.Spec.InitContainers) != 2 {
		t.Fatalf("expected 2 initContainers, got %d", len(pod.Spec.InitContainers))
	}
	initExternals := pod.Spec.InitContainers[0]
	if initExternals.Name != DindInitExternalsName {
		t.Errorf("expected initContainer %s, got %s", DindInitExternalsName, initExternals.Name)
	}
	if initExternals.Image != "ghcr.io/actions/actions-runner:2.336.0" {
		t.Errorf("expected initContainer image to match runner image, got %s", initExternals.Image)
	}

	dindC := pod.Spec.InitContainers[1]
	if dindC.Name != DindContainerName {
		t.Errorf("expected initContainer %s, got %s", DindContainerName, dindC.Name)
	}
	if dindC.Image != DefaultDindImage {
		t.Errorf("expected dind image %s, got %s", DefaultDindImage, dindC.Image)
	}
	if dindC.RestartPolicy == nil || *dindC.RestartPolicy != corev1.ContainerRestartPolicyAlways {
		t.Errorf("expected restartPolicy Always for dind sidecar")
	}
	if dindC.SecurityContext == nil || dindC.SecurityContext.Privileged == nil || !*dindC.SecurityContext.Privileged {
		t.Errorf("expected privileged true for dind sidecar")
	}

	// 2. Verify volumes
	volMap := make(map[string]bool)
	for _, v := range pod.Spec.Volumes {
		volMap[v.Name] = true
	}
	for _, expectedVol := range []string{VolumeWorkName, VolumeDindSockName, VolumeDindExternalsName, VolumeDockerStorageName} {
		if !volMap[expectedVol] {
			t.Errorf("missing volume %s", expectedVol)
		}
	}

	// 3. Verify runner container mounts & env
	c := pod.Spec.Containers[0]
	mountMap := make(map[string]string)
	for _, vm := range c.VolumeMounts {
		mountMap[vm.Name] = vm.MountPath
	}
	if mountMap[VolumeWorkName] != "/home/runner/_work" {
		t.Errorf("expected %s mounted at /home/runner/_work, got %s", VolumeWorkName, mountMap[VolumeWorkName])
	}
	if mountMap[VolumeDindSockName] != "/var/run" {
		t.Errorf("expected %s mounted at /var/run, got %s", VolumeDindSockName, mountMap[VolumeDindSockName])
	}

	envMap := make(map[string]string)
	for _, e := range c.Env {
		envMap[e.Name] = e.Value
	}
	if envMap[EnvDockerHost] != DefaultDindSocketPath {
		t.Errorf("expected %s=%s, got %s", EnvDockerHost, DefaultDindSocketPath, envMap[EnvDockerHost])
	}
	if envMap[EnvRunnerWaitForDocker] != "120" {
		t.Errorf("expected %s=120, got %s", EnvRunnerWaitForDocker, envMap[EnvRunnerWaitForDocker])
	}
}

func TestBuildRunnerPod_KubernetesMode(t *testing.T) {
	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "test-scaleset",
		UID:  "scaleset-uid-123",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			ContainerMode: ghav1alpha1.ContainerModeKubernetes,
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
	if len(pod.Spec.InitContainers) != 0 {
		t.Errorf("expected 0 initContainers in kubernetes mode, got %d", len(pod.Spec.InitContainers))
	}
}

func TestBuildRunnerPod_CustomDinDSpec(t *testing.T) {
	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "test-scaleset",
		UID:  "scaleset-uid-123",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			ContainerMode: ghav1alpha1.ContainerModeDind,
			DinD: &ghav1alpha1.DinDSpec{
				Image:          "docker:26-dind",
				DockerGroupGID: "999",
				MTU:            "1400",
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
	if len(pod.Spec.InitContainers) != 2 {
		t.Fatalf("expected 2 initContainers, got %d", len(pod.Spec.InitContainers))
	}
	dindC := pod.Spec.InitContainers[1]
	if dindC.Image != "docker:26-dind" {
		t.Errorf("expected custom image docker:26-dind, got %s", dindC.Image)
	}

	foundMTU := false
	for _, arg := range dindC.Args {
		if arg == "--mtu=1400" {
			foundMTU = true
		}
	}
	if !foundMTU {
		t.Errorf("expected --mtu=1400 in args, got %v", dindC.Args)
	}

	foundGID := false
	for _, env := range dindC.Env {
		if env.Name == EnvDockerGroupGID && env.Value == "999" {
			foundGID = true
		}
	}
	if !foundGID {
		t.Errorf("expected DOCKER_GROUP_GID=999, got %v", dindC.Env)
	}
}

func TestBuildRunnerPod_MetricsInjection(t *testing.T) {
	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "test-scaleset",
		UID:  "scaleset-uid-123",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			ContainerMode: ghav1alpha1.ContainerModeKubernetes,
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
