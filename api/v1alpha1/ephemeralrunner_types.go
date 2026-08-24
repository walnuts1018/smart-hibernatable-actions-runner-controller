/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EphemeralRunnerPhase represents the lifecycle phase of an ephemeral runner.
// +kubebuilder:validation:Enum=Pending;WaitingForCluster;Provisioning;Starting;Idle;Busy;Completed;Failed;Deleting
type EphemeralRunnerPhase string

// EphemeralRunner phases.
const (
	EphemeralRunnerPhasePending           EphemeralRunnerPhase = "Pending"
	EphemeralRunnerPhaseWaitingForCluster EphemeralRunnerPhase = "WaitingForCluster"
	EphemeralRunnerPhaseProvisioning      EphemeralRunnerPhase = "Provisioning"
	EphemeralRunnerPhaseStarting          EphemeralRunnerPhase = "Starting"
	EphemeralRunnerPhaseIdle              EphemeralRunnerPhase = "Idle"
	EphemeralRunnerPhaseBusy              EphemeralRunnerPhase = "Busy"
	EphemeralRunnerPhaseCompleted         EphemeralRunnerPhase = "Completed"
	EphemeralRunnerPhaseFailed            EphemeralRunnerPhase = "Failed"
	EphemeralRunnerPhaseDeleting          EphemeralRunnerPhase = "Deleting"
)

// EphemeralRunnerSpec defines the desired state of EphemeralRunner.
type EphemeralRunnerSpec struct {
	// ScaleSetRef references the parent RunnerScaleSet.
	// +kubebuilder:validation:Required
	ScaleSetRef corev1.LocalObjectReference `json:"scaleSetRef"`

	// RunnerName is the unique name of this runner instance registered in GitHub Actions and used for the remote Pod.
	// +kubebuilder:validation:Required
	RunnerName string `json:"runnerName"`
}

// GitHubRunnerStatus records runner IDs assigned by GitHub Actions.
type GitHubRunnerStatus struct {
	// RunnerID is the runner ID registered in GitHub Actions.
	// +optional
	RunnerID int64 `json:"runnerID,omitempty"`

	// RunnerRequestID is the ID of the runner request in GitHub Actions.
	// +optional
	RunnerRequestID int64 `json:"runnerRequestID,omitempty"`

	// JobID is the ID of the job assigned to this runner.
	// +optional
	JobID int64 `json:"jobID,omitempty"`

	// StartedObservedAt records the timestamp when the JobStarted event was observed from GitHub Actions.
	// +optional
	StartedObservedAt *metav1.Time `json:"startedObservedAt,omitempty"`

	// CompletedObserved indicates whether a JobCompleted event was received from GitHub Actions.
	// +optional
	CompletedObserved bool `json:"completedObserved,omitempty"`

	// CompletedObservedAt records the timestamp when the JobCompleted event was observed.
	// +optional
	CompletedObservedAt *metav1.Time `json:"completedObservedAt,omitempty"`

	// CompletedResult records the result status string ("completed", "canceled", "failed", etc.) reported by GitHub.
	// +optional
	CompletedResult string `json:"completedResult,omitempty"`
}

// RemotePodStatus records metadata about the runner Pod running on the remote cluster.
type RemotePodStatus struct {
	// Namespace is the namespace where the Pod was created on the remote cluster.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Name is the name of the runner Pod on the remote cluster.
	// +optional
	Name string `json:"name,omitempty"`

	// UID is the UID of the runner Pod on the remote cluster.
	// +optional
	UID string `json:"uid,omitempty"`

	// NodeName is the name of the remote node hosting the runner Pod.
	// +optional
	NodeName string `json:"nodeName,omitempty"`
}

// ProvisioningAttemptStatus tracks an individual JIT provisioning attempt to prevent orphan runners and handle conflicts.
type ProvisioningAttemptStatus struct {
	// ID is the unique attempt identifier (e.g. short hash or sequential counter).
	// +optional
	ID string `json:"id,omitempty"`

	// RunnerName is the actual runner name registered in GitHub for this attempt.
	// +optional
	RunnerName string `json:"runnerName,omitempty"`

	// RunnerID is the GitHub Runner ID assigned after JIT generation.
	// +optional
	RunnerID int64 `json:"runnerID,omitempty"`

	// StartedAt is the timestamp when this provisioning attempt started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// JITGeneratedAt is the timestamp when JIT config was generated.
	// +optional
	JITGeneratedAt *metav1.Time `json:"jitGeneratedAt,omitempty"`

	// Failures is the count of consecutive failed JIT provisioning attempts.
	// +optional
	Failures int32 `json:"failures,omitempty"`

	// NextRetryAt is the timestamp when the next JIT provisioning attempt is allowed.
	// +optional
	NextRetryAt *metav1.Time `json:"nextRetryAt,omitempty"`
}

// RunnerFailureStatus records details when a runner pod or execution failed.
type RunnerFailureStatus struct {
	// Reason is a brief camelCase string indicating the failure reason.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message is a human-readable description of the failure.
	// +optional
	Message string `json:"message,omitempty"`

	// ExitCode is the container exit code if available.
	// +optional
	ExitCode int32 `json:"exitCode,omitempty"`
}

// EphemeralRunnerStatus defines the observed state of EphemeralRunner.
type EphemeralRunnerStatus struct {
	// Phase is the current execution phase of the ephemeral runner.
	// +kubebuilder:default="Pending"
	// +optional
	Phase EphemeralRunnerPhase `json:"phase,omitempty"`

	// Provisioning records details of the active or latest JIT provisioning attempt.
	// +optional
	Provisioning *ProvisioningAttemptStatus `json:"provisioning,omitempty"`

	// GitHub holds IDs and job details from GitHub Actions.
	// +optional
	GitHub GitHubRunnerStatus `json:"github,omitempty"`

	// RemotePod holds details of the pod running on the remote cluster.
	// +optional
	RemotePod RemotePodStatus `json:"remotePod,omitempty"`

	// FinishedAt is the timestamp when the runner reached a terminal state (Completed or Failed).
	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`

	// GCEligibleAt is the timestamp after which this EphemeralRunner CR can be garbage collected.
	// +optional
	GCEligibleAt *metav1.Time `json:"gcEligibleAt,omitempty"`

	// Failure contains diagnostic information if the runner failed.
	// +optional
	Failure *RunnerFailureStatus `json:"failure,omitempty"`

	// Conditions store the detailed status conditions of the runner.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=eprunner;er,categories=gha;all
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase",description="Ephemeral runner phase"
// +kubebuilder:printcolumn:name="RunnerID",type="integer",JSONPath=".status.github.runnerID",description="GitHub Runner ID"
// +kubebuilder:printcolumn:name="Remote Pod",type="string",JSONPath=".status.remotePod.name",description="Remote Pod name"
// +kubebuilder:printcolumn:name="Node",type="string",JSONPath=".status.remotePod.nodeName",description="Remote Node name"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// EphemeralRunner is the Schema for the ephemeralrunners API.
type EphemeralRunner struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EphemeralRunnerSpec   `json:"spec,omitempty"`
	Status EphemeralRunnerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EphemeralRunnerList contains a list of EphemeralRunner.
type EphemeralRunnerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EphemeralRunner `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &EphemeralRunner{}, &EphemeralRunnerList{})
		return nil
	})
}
