package runner

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

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
	FinalizerRunnerCleanup         = "gha.walnuts.dev/remote-runner-cleanup"
	FinalizerScaleSetCleanup       = "gha.walnuts.dev/runner-scale-set"
)

// GenerateRunnerName creates a unique runner name from the scale set name.
func GenerateRunnerName(scaleSetName string) string {
	prefix := scaleSetName
	if len(prefix) > 48 {
		prefix = prefix[:48]
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

// JitSecretName returns the name of the secret holding the JIT token for the runner.
func JitSecretName(runnerName string) string {
	return fmt.Sprintf("%s-jit", runnerName)
}
