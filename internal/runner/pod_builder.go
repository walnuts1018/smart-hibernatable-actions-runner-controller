package runner

import (
	"maps"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

// BuildRunnerPod constructs the ephemeral runner Pod for execution on the remote cluster.
func BuildRunnerPod(namespace string, scaleSet *ghav1alpha1.RunnerScaleSet, runner *ghav1alpha1.EphemeralRunner) *corev1.Pod {
	template := scaleSet.Spec.Runner.Template
	podSpec := template.Spec.DeepCopy()

	podSpec.RestartPolicy = corev1.RestartPolicyNever

	targetContainerName := scaleSet.Spec.Runner.ContainerName
	if targetContainerName == "" {
		targetContainerName = DefaultContainerName
	}

	jitSecretName := JitSecretName(runner.Spec.RunnerName)
	jitEnvVar := corev1.EnvVar{
		Name: EnvJitConfig,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: jitSecretName,
				},
				Key: JitConfigSecretKey,
			},
		},
	}

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
		ObjectMeta: metav1.ObjectMeta{
			Name:        runner.Spec.RunnerName,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: *podSpec,
	}
}
