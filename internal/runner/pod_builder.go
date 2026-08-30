package runner

import (
	"encoding/json"
	"fmt"
	"maps"

	corev1 "k8s.io/api/core/v1"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

// BuildRunnerPod constructs the ephemeral runner Pod for execution on the remote cluster.
func BuildRunnerPod(namespace string, scaleSet *ghav1alpha1.RunnerScaleSet, runner *ghav1alpha1.EphemeralRunner) *corev1.Pod {
	template := scaleSet.Spec.Runner.Template
	podSpec := template.Spec.DeepCopy()

	podSpec.RestartPolicy = corev1.RestartPolicyNever

	// デフォルトで不要な ServiceAccount Token マウントと ServiceLinks を無効化
	if podSpec.AutomountServiceAccountToken == nil {
		f := false
		podSpec.AutomountServiceAccountToken = &f
	}
	if podSpec.EnableServiceLinks == nil {
		f := false
		podSpec.EnableServiceLinks = &f
	}

	targetContainerName := DefaultContainerName

	jitSecretName := JitSecretName(runner.Spec.RunnerName)
	jitEnvVar := corev1.EnvVar{
		Name: EnvJitConfig,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				Name: jitSecretName,
				Key:  JitConfigSecretKey,
			},
		},
	}

	injectDinD(&scaleSet.Spec.Runner, podSpec)
	injectMetrics(&scaleSet.Spec.Runner, podSpec)

	containerFound := false
	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name == targetContainerName {
			podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, jitEnvVar)
			containerFound = true
			break
		}
	}

	if !containerFound && len(podSpec.Containers) > 0 {
		// Fallback to first container
		podSpec.Containers[0].Env = append(podSpec.Containers[0].Env, jitEnvVar)
	}

	// Merge labels
	labels := make(map[string]string)
	maps.Copy(labels, template.Labels)
	labels[LabelManagedBy] = LabelManagedByValue
	labels[LabelScaleSetUID] = string(scaleSet.UID)
	labels[LabelScaleSetName] = scaleSet.Name
	labels[LabelRunnerUID] = string(runner.UID)
	labels[LabelRunnerName] = runner.Spec.RunnerName

	annotations := make(map[string]string)
	maps.Copy(annotations, template.Annotations)

	return &corev1.Pod{
		Name:        runner.Spec.RunnerName,
		Namespace:   namespace,
		Labels:      labels,
		Annotations: annotations,
		Spec:        *podSpec,
	}
}

func injectMetrics(runnerSpec *ghav1alpha1.RunnerTemplateSpec, podSpec *corev1.PodSpec) {
	if runnerSpec.Metrics == nil || !runnerSpec.Metrics.Enabled {
		return
	}

	hookImage := runnerSpec.Metrics.Image
	if hookImage == "" {
		hookImage = DefaultRunnerHookImage
	}

	// 1. Add initContainer for copying runner-hook binary
	initContainer := corev1.Container{
		Name:    RunnerHookInitContainerName,
		Image:   hookImage,
		Command: []string{"/runner-hook", "install", RunnerHookMountPath},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      RunnerHookVolumeName,
				MountPath: RunnerHookMountPath,
			},
		},
	}
	podSpec.InitContainers = append([]corev1.Container{initContainer}, podSpec.InitContainers...)

	// 2. Add volumes
	hostPathDirectory := corev1.HostPathDirectory
	podSpec.Volumes = append(podSpec.Volumes,
		corev1.Volume{
			Name:     RunnerHookVolumeName,
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
		corev1.Volume{
			Name: CgroupRootVolumeName,
			HostPath: &corev1.HostPathVolumeSource{
				Path: CgroupRootHostPath,
				Type: &hostPathDirectory,
			},
		},
	)

	// 3. Prepare env vars
	envVars := []corev1.EnvVar{
		{
			Name:  EnvActionsRunnerHookJobStarted,
			Value: RunnerHookMountPath + "/job-started",
		},
		{
			Name:  EnvActionsRunnerHookJobCompleted,
			Value: RunnerHookMountPath + "/job-completed",
		},
		{
			Name:  EnvRunnerMetricsCgroupRoot,
			Value: CgroupRootMountPath,
		},
		{
			Name: EnvPodUID,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.uid",
				},
			},
		},
		{
			Name: EnvPodName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name: EnvPodNamespace,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		{
			Name: EnvNodeName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "spec.nodeName",
				},
			},
		},
	}

	if runnerSpec.Metrics.Endpoint != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  EnvRunnerMetricsEndpoint,
			Value: runnerSpec.Metrics.Endpoint,
		})
	}

	if len(runnerSpec.Metrics.ExtraAttributes) > 0 {
		if raw, err := json.Marshal(runnerSpec.Metrics.ExtraAttributes); err == nil {
			envVars = append(envVars, corev1.EnvVar{
				Name:  EnvRunnerMetricsExtraAttrs,
				Value: string(raw),
			})
		}
	}

	volumeMounts := []corev1.VolumeMount{
		{
			Name:      RunnerHookVolumeName,
			MountPath: RunnerHookMountPath,
			ReadOnly:  true,
		},
		{
			Name:      CgroupRootVolumeName,
			MountPath: CgroupRootMountPath,
			ReadOnly:  true,
		},
	}

	// 4. Inject into runner container (or fallback to first container)
	targetContainerName := DefaultContainerName
	containerFound := false
	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name == targetContainerName {
			podSpec.Containers[i].VolumeMounts = append(podSpec.Containers[i].VolumeMounts, volumeMounts...)
			podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, envVars...)
			containerFound = true
			break
		}
	}

	if !containerFound && len(podSpec.Containers) > 0 {
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, volumeMounts...)
		podSpec.Containers[0].Env = append(podSpec.Containers[0].Env, envVars...)
	}
}

func injectDinD(runnerSpec *ghav1alpha1.RunnerTemplateSpec, podSpec *corev1.PodSpec) {
	// If ContainerMode is explicitly set to kubernetes, skip DinD injection.
	if runnerSpec.ContainerMode == ghav1alpha1.ContainerModeKubernetes {
		return
	}
	if runnerSpec.DinD != nil && runnerSpec.DinD.Enabled != nil && !*runnerSpec.DinD.Enabled {
		return
	}

	runnerImage := getRunnerImage(podSpec)
	dindContainer := buildDindContainer(runnerSpec)

	injectDindInitContainers(podSpec, runnerImage, dindContainer)
	injectDindVolumes(podSpec)
	injectDindRunnerMountsAndEnv(podSpec)
}

func getRunnerImage(podSpec *corev1.PodSpec) string {
	for _, c := range podSpec.Containers {
		if c.Name == DefaultContainerName && c.Image != "" {
			return c.Image
		}
	}
	if len(podSpec.Containers) > 0 && podSpec.Containers[0].Image != "" {
		return podSpec.Containers[0].Image
	}
	return DefaultActionsRunnerImage
}

func buildDindContainer(runnerSpec *ghav1alpha1.RunnerTemplateSpec) corev1.Container {
	dindImage := DefaultDindImage
	groupGID := DefaultDindGroupGID
	var dindRes corev1.ResourceRequirements
	var dindArgs []string
	var dindEnv []corev1.EnvVar
	var dindSecCtx *corev1.SecurityContext

	if runnerSpec.DinD != nil {
		if runnerSpec.DinD.Image != "" {
			dindImage = runnerSpec.DinD.Image
		}
		if runnerSpec.DinD.DockerGroupGID != "" {
			groupGID = runnerSpec.DinD.DockerGroupGID
		}
		if runnerSpec.DinD.Resources != nil {
			dindRes = *runnerSpec.DinD.Resources
		}
		dindArgs = runnerSpec.DinD.Args
		dindEnv = append([]corev1.EnvVar{}, runnerSpec.DinD.Env...)
		dindSecCtx = runnerSpec.DinD.SecurityContext
	}

	if len(dindArgs) == 0 {
		dindArgs = []string{
			"dockerd",
			"--host=unix:///var/run/docker.sock",
			"--group=$(DOCKER_GROUP_GID)",
		}
		if runnerSpec.DinD != nil && runnerSpec.DinD.MTU != "" {
			dindArgs = append(dindArgs, fmt.Sprintf("--mtu=%s", runnerSpec.DinD.MTU))
		}
	}

	if dindSecCtx == nil {
		priv := true
		dindSecCtx = &corev1.SecurityContext{
			Privileged: &priv,
		}
	}

	hasGroupGID := false
	for _, e := range dindEnv {
		if e.Name == EnvDockerGroupGID {
			hasGroupGID = true
			break
		}
	}
	if !hasGroupGID {
		dindEnv = append(dindEnv, corev1.EnvVar{
			Name:  EnvDockerGroupGID,
			Value: groupGID,
		})
	}

	restartAlways := corev1.ContainerRestartPolicyAlways
	return corev1.Container{
		Name:            DindContainerName,
		Image:           dindImage,
		Args:            dindArgs,
		Env:             dindEnv,
		Resources:       dindRes,
		SecurityContext: dindSecCtx,
		RestartPolicy:   &restartAlways,
		StartupProbe: &corev1.Probe{
			Exec: &corev1.ExecAction{
				Command: []string{"docker", "info"},
			},
			InitialDelaySeconds: 0,
			PeriodSeconds:       5,
			TimeoutSeconds:      10,
			FailureThreshold:    24,
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: VolumeWorkName, MountPath: "/home/runner/_work"},
			{Name: VolumeDindSockName, MountPath: "/var/run"},
			{Name: VolumeDindExternalsName, MountPath: "/home/runner/externals"},
			{Name: VolumeDockerStorageName, MountPath: "/var/lib/docker"},
		},
	}
}

func injectDindInitContainers(podSpec *corev1.PodSpec, runnerImage string, dindContainer corev1.Container) {
	hasInitExternals := false
	for _, ic := range podSpec.InitContainers {
		if ic.Name == DindInitExternalsName {
			hasInitExternals = true
			break
		}
	}
	if !hasInitExternals {
		initExternals := corev1.Container{
			Name:    DindInitExternalsName,
			Image:   runnerImage,
			Command: []string{"cp", "-r", "/home/runner/externals/.", "/home/runner/tmpDir/"},
			VolumeMounts: []corev1.VolumeMount{
				{Name: VolumeDindExternalsName, MountPath: "/home/runner/tmpDir"},
			},
		}
		podSpec.InitContainers = append([]corev1.Container{initExternals}, podSpec.InitContainers...)
	}

	hasDind := false
	for _, ic := range podSpec.InitContainers {
		if ic.Name == DindContainerName {
			hasDind = true
			break
		}
	}
	if !hasDind {
		for _, c := range podSpec.Containers {
			if c.Name == DindContainerName {
				hasDind = true
				break
			}
		}
	}
	if !hasDind {
		podSpec.InitContainers = append(podSpec.InitContainers, dindContainer)
	}
}

func injectDindVolumes(podSpec *corev1.PodSpec) {
	dindVolumes := []corev1.Volume{
		{Name: VolumeWorkName, EmptyDir: &corev1.EmptyDirVolumeSource{}},
		{Name: VolumeDindSockName, EmptyDir: &corev1.EmptyDirVolumeSource{}},
		{Name: VolumeDindExternalsName, EmptyDir: &corev1.EmptyDirVolumeSource{}},
		{Name: VolumeDockerStorageName, EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}
	for _, dv := range dindVolumes {
		exists := false
		for _, v := range podSpec.Volumes {
			if v.Name == dv.Name {
				exists = true
				break
			}
		}
		if !exists {
			podSpec.Volumes = append(podSpec.Volumes, dv)
		}
	}
}

func injectDindRunnerMountsAndEnv(podSpec *corev1.PodSpec) {
	runnerMounts := []corev1.VolumeMount{
		{Name: VolumeWorkName, MountPath: "/home/runner/_work"},
		{Name: VolumeDindSockName, MountPath: "/var/run"},
	}
	runnerEnvs := []corev1.EnvVar{
		{Name: EnvDockerHost, Value: DefaultDindSocketPath},
		{Name: EnvRunnerWaitForDocker, Value: "120"},
	}

	injectIntoContainer := func(c *corev1.Container) {
		for _, rm := range runnerMounts {
			mountExists := false
			for _, existingM := range c.VolumeMounts {
				if existingM.Name == rm.Name || existingM.MountPath == rm.MountPath {
					mountExists = true
					break
				}
			}
			if !mountExists {
				c.VolumeMounts = append(c.VolumeMounts, rm)
			}
		}
		for _, re := range runnerEnvs {
			envExists := false
			for _, existingE := range c.Env {
				if existingE.Name == re.Name {
					envExists = true
					break
				}
			}
			if !envExists {
				c.Env = append(c.Env, re)
			}
		}
	}

	containerFound := false
	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name == DefaultContainerName {
			injectIntoContainer(&podSpec.Containers[i])
			containerFound = true
			break
		}
	}
	if !containerFound && len(podSpec.Containers) > 0 {
		injectIntoContainer(&podSpec.Containers[0])
	}
}
