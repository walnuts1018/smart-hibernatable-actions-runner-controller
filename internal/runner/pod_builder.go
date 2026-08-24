package runner

import (
	"encoding/json"
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

	injectMetrics(scaleSet, podSpec)

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

func injectMetrics(scaleSet *ghav1alpha1.RunnerScaleSet, podSpec *corev1.PodSpec) {
	if scaleSet.Spec.Metrics == nil || !scaleSet.Spec.Metrics.Enabled {
		return
	}

	hookImage := scaleSet.Spec.Metrics.Image
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

	if scaleSet.Spec.Metrics.Endpoint != "" {
		envVars = append(envVars, corev1.EnvVar{
			Name:  EnvRunnerMetricsEndpoint,
			Value: scaleSet.Spec.Metrics.Endpoint,
		})
	}

	if len(scaleSet.Spec.Metrics.ExtraAttributes) > 0 {
		if raw, err := json.Marshal(scaleSet.Spec.Metrics.ExtraAttributes); err == nil {
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
