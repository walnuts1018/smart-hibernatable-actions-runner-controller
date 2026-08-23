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

// GitHubScaleSetSpec defines GitHub Actions connection and scale set target settings.
type GitHubScaleSetSpec struct {
	// ConfigURL is the GitHub URL for the repository or organization (e.g., https://github.com/my-org or https://github.com/my-org/my-repo).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="configURL is immutable"
	ConfigURL string `json:"configURL"`

	// ScaleSetName is the name of the RunnerScaleSet registered in GitHub Actions.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="scaleSetName is immutable"
	ScaleSetName string `json:"scaleSetName"`

	// RunnerGroup is the GitHub runner group name (default: "default").
	// +kubebuilder:default="default"
	// +optional
	RunnerGroup string `json:"runnerGroup,omitempty"`

	// CredentialsSecretRef references the Secret containing GitHub App credentials (github_app_id, github_app_installation_id, github_app_private_key).
	// +kubebuilder:validation:Required
	CredentialsSecretRef corev1.LocalObjectReference `json:"credentialsSecretRef"`
}

// RunnerScaleSetScalingSpec defines the autoscaling runner limits for the scale set.
// +kubebuilder:validation:XValidation:rule="self.minRunners <= self.maxRunners",message="minRunners must be less than or equal to maxRunners"
type RunnerScaleSetScalingSpec struct {
	// MinRunners is the minimum number of idle/standby runners to maintain.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinRunners int32 `json:"minRunners"`

	// MaxRunners is the maximum number of concurrent runner pods allowed for this scale set.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxRunners int32 `json:"maxRunners"`
}

// RunnerTemplateSpec defines the configuration of the ephemeral runner Pod.
type RunnerTemplateSpec struct {
	// ContainerName is the name of the primary runner container in the pod template.
	// +kubebuilder:default="runner"
	// +optional
	ContainerName string `json:"containerName,omitempty"`

	// WorkDir is the working directory for runner jobs.
	// +kubebuilder:default="_work"
	// +optional
	WorkDir string `json:"workDir,omitempty"`

	// Template is the pod template for executing ephemeral runner workloads on the remote cluster.
	// +kubebuilder:validation:Required
	Template corev1.PodTemplateSpec `json:"template"`
}

// ListenerSpec defines configuration for the runner scale set listener deployment.
type ListenerSpec struct {
	// Resources defines resource requests and limits for the listener container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// RunnerScaleSetSpec defines the desired state of RunnerScaleSet.
type RunnerScaleSetSpec struct {
	// Suspend suspends runner scaling and causes the listener to advertise 0 capacity.
	// +kubebuilder:default=false
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// GitHub specifies the GitHub Actions connection and scale set target settings.
	// +kubebuilder:validation:Required
	GitHub GitHubScaleSetSpec `json:"github"`

	// NodePoolRef references the RunnerNodePool providing physical machine capacity for this scale set.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodePoolRef is immutable"
	NodePoolRef corev1.LocalObjectReference `json:"nodePoolRef"`

	// Scaling defines the runner scaling bounds.
	// +optional
	Scaling RunnerScaleSetScalingSpec `json:"scaling,omitempty"`

	// Listener defines the configuration of the background listener deployment.
	// +optional
	Listener ListenerSpec `json:"listener,omitempty"`

	// Runner defines the runner pod template and container settings.
	// +kubebuilder:validation:Required
	Runner RunnerTemplateSpec `json:"runner"`
}

// GitHubStatisticsStatus stores the latest runner statistics received from the GitHub Actions service.
type GitHubStatisticsStatus struct {
	// AssignedJobs is the count of jobs currently assigned to this scale set.
	// +optional
	AssignedJobs int32 `json:"assignedJobs"`

	// RunningJobs is the count of jobs currently running in this scale set.
	// +optional
	RunningJobs int32 `json:"runningJobs"`

	// RegisteredRunners is the count of runners registered to this scale set in GitHub.
	// +optional
	RegisteredRunners int32 `json:"registeredRunners"`

	// BusyRunners is the count of registered runners currently executing a job.
	// +optional
	BusyRunners int32 `json:"busyRunners"`

	// IdleRunners is the count of registered runners currently idle.
	// +optional
	IdleRunners int32 `json:"idleRunners"`

	// LastStatisticsTime is the timestamp when statistics were last refreshed from GitHub.
	// +optional
	LastStatisticsTime *metav1.Time `json:"lastStatisticsTime,omitempty"`
}

// ListenerStatus defines the status of the Scale Set Listener.
type ListenerStatus struct {
	// Ready indicates whether the listener deployment is running and connected to GitHub Actions.
	// +optional
	Ready bool `json:"ready"`

	// LastConnectedTime is the timestamp of the last successful session connection to GitHub Actions.
	// +optional
	LastConnectedTime *metav1.Time `json:"lastConnectedTime,omitempty"`

	// LastPollTime is the timestamp of the latest successful message session poll or heartbeat.
	// +optional
	LastPollTime *metav1.Time `json:"lastPollTime,omitempty"`
}

// RunnerScaleSetStatus defines the observed state of RunnerScaleSet.
type RunnerScaleSetStatus struct {
	// ScaleSetID is the numeric ID assigned to this scale set by GitHub Actions.
	// +optional
	ScaleSetID int64 `json:"scaleSetID,omitempty"`

	// EffectiveMaxRunners is the dynamically calculated upper limit of runners (min of Spec.Scaling.MaxRunners and NodePool capacity, or 0 if suspended).
	// +optional
	EffectiveMaxRunners int32 `json:"effectiveMaxRunners"`

	// DesiredRunners is the calculated target number of ephemeral runners to satisfy demand.
	// +optional
	DesiredRunners int32 `json:"desiredRunners"`

	// ActiveRunners is the count of non-terminal EphemeralRunner custom resources currently tracked.
	// +optional
	ActiveRunners int32 `json:"activeRunners"`

	// GitHub holds statistics received directly from the GitHub Actions message session.
	// +optional
	GitHub GitHubStatisticsStatus `json:"github,omitempty"`

	// Listener holds the observed status of the background listener deployment.
	// +optional
	Listener ListenerStatus `json:"listener,omitempty"`

	// Conditions store the detailed status conditions of the scale set.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rscaleset;rss,categories=gha;all
// +kubebuilder:printcolumn:name="ScaleSetID",type="integer",JSONPath=".status.scaleSetID",description="GitHub ScaleSet ID"
// +kubebuilder:printcolumn:name="Desired",type="integer",JSONPath=".status.desiredRunners",description="Desired runners count"
// +kubebuilder:printcolumn:name="Active",type="integer",JSONPath=".status.activeRunners",description="Active runners count"
// +kubebuilder:printcolumn:name="Assigned",type="integer",JSONPath=".status.github.assignedJobs",description="Assigned jobs count"
// +kubebuilder:printcolumn:name="Busy",type="integer",JSONPath=".status.github.busyRunners",description="Busy runners count"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// RunnerScaleSet is the Schema for the runnerscalesets API.
type RunnerScaleSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RunnerScaleSetSpec   `json:"spec,omitempty"`
	Status RunnerScaleSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RunnerScaleSetList contains a list of RunnerScaleSet.
type RunnerScaleSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RunnerScaleSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &RunnerScaleSet{}, &RunnerScaleSetList{})
		return nil
	})
}
