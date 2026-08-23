package runner

import (
	"time"

	corev1 "k8s.io/api/core/v1"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

// JIT configuration constants.
const (
	JitConfigSecretKey = "jitconfig"
	EnvJitConfig       = "ACTIONS_RUNNER_INPUT_JITCONFIG"
)

// BuildJitSecret constructs the Secret containing the encoded JIT runner config for the remote cluster.
func BuildJitSecret(namespace string, runner *ghav1alpha1.EphemeralRunner, jitConfig string) *corev1.Secret {
	secretName := JitSecretName(runner.Spec.RunnerName)
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	return &corev1.Secret{
		Name:      secretName,
		Namespace: namespace,
		Labels: map[string]string{
			LabelManagedBy:    LabelManagedByValue,
			LabelRunnerUID:    string(runner.UID),
			LabelRunnerName:   runner.Spec.RunnerName,
			LabelScaleSetName: runner.Spec.ScaleSetRef.Name,
		},
		Annotations: map[string]string{
			AnnotationExpiresAt: expiresAt,
		},
		Type:      corev1.SecretTypeOpaque,
		Immutable: new(true),
		StringData: map[string]string{
			JitConfigSecretKey: jitConfig,
		},
	}
}
