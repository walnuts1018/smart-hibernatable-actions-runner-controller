package runner

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Well-known labels, annotations, and finalizers for runner resources.
const (
	LabelManagedBy                 = "app.kubernetes.io/managed-by"
	LabelManagedByValue            = "gha-baremetal-operator"
	LabelScaleSetUID               = "gha.walnuts.dev/scale-set-uid"
	LabelScaleSetName              = "gha.walnuts.dev/scale-set-name"
	LabelRunnerUID                 = "gha.walnuts.dev/runner-uid"
	LabelRunnerName                = "gha.walnuts.dev/runner-name"
	AnnotationCordonedBy           = "gha.walnuts.dev/cordoned-by"
	AnnotationCredentialsHash      = "gha.walnuts.dev/credentials-hash"
	AnnotationExpiresAt            = "gha.walnuts.dev/expires-at"
	AnnotationOrphanGitHubResource = "gha.walnuts.dev/orphan-github-resource"
	AnnotationAdoptMachineID       = "gha.walnuts.dev/adopt-machine-id"
	FinalizerRunnerCleanup         = "gha.walnuts.dev/remote-runner-cleanup"
	FinalizerScaleSetCleanup       = "gha.walnuts.dev/runner-scale-set"
	IndexGitHubRunnerName          = "gha.walnuts.dev/github-runner-name"
	DefaultContainerName           = "runner"
	DefaultRunnerNamespace         = "gha-runners"
)

// GenerateRunnerName creates a unique runner name from the scale set name.
func GenerateRunnerName(scaleSetName string) string {
	prefix := scaleSetName
	if len(prefix) > 48 {
		prefix = prefix[:48]
	}
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s-%x", prefix, b)
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

// JitSecretName returns the name of the secret holding the JIT token for the runner.
func JitSecretName(runnerName string) string {
	return fmt.Sprintf("%s-jit", runnerName)
}
