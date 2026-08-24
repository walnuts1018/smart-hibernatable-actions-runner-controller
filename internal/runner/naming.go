package runner

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Well-known labels, annotations, and finalizers for runner resources.
const (
	LabelManagedBy                 = "app.kubernetes.io/managed-by"
	LabelManagedByValue            = "smart-hibernatable-actions-runner-controller"
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
	DefaultWorkDir                 = "_work"
	DefaultRunnerNamespace         = "gha-runners"
	DefaultListenerImage           = "ghcr.io/walnuts1018/smart-hibernatable-actions-runner-controller/listener:latest"
	DefaultRunnerHookImage         = "ghcr.io/walnuts1018/smart-hibernatable-actions-runner-controller/runner-hook:latest"

	RunnerHookVolumeName        = "runner-hook-bin"
	RunnerHookMountPath         = "/opt/runner-hook"
	RunnerHookInitContainerName = "init-runner-hook"
	CgroupRootVolumeName        = "cgroup-root"
	CgroupRootHostPath          = "/sys/fs/cgroup"
	CgroupRootMountPath         = "/host/sys/fs/cgroup"

	DefaultDindImage          = "docker:dind"
	DefaultDindGroupGID       = "123"
	DefaultDindSocketPath     = "unix:///var/run/docker.sock"
	DefaultActionsRunnerImage = "ghcr.io/actions/actions-runner:latest"

	DindContainerName       = "dind"
	DindInitExternalsName   = "init-dind-externals"
	VolumeWorkName          = "work"
	VolumeDindSockName      = "dind-sock"
	VolumeDindExternalsName = "dind-externals"
	VolumeDockerStorageName = "docker-storage"

	EnvActionsRunnerHookJobStarted   = "ACTIONS_RUNNER_HOOK_JOB_STARTED"
	EnvActionsRunnerHookJobCompleted = "ACTIONS_RUNNER_HOOK_JOB_COMPLETED"
	EnvRunnerMetricsEndpoint         = "RUNNER_METRICS_ENDPOINT"
	EnvRunnerMetricsCgroupRoot       = "RUNNER_METRICS_CGROUP_ROOT"
	EnvRunnerMetricsExtraAttrs       = "RUNNER_METRICS_EXTRA_ATTRIBUTES"
	EnvPodUID                        = "POD_UID"
	EnvPodName                       = "POD_NAME"
	EnvPodNamespace                  = "POD_NAMESPACE"
	EnvNodeName                      = "NODE_NAME"
	EnvDockerHost                    = "DOCKER_HOST"
	EnvDockerGroupGID                = "DOCKER_GROUP_GID"
	EnvRunnerWaitForDocker           = "RUNNER_WAIT_FOR_DOCKER_IN_SECONDS"
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
