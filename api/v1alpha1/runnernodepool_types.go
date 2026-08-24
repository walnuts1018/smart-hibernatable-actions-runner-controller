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

// RunnerNodePoolScalingSpec defines autoscaling parameters for physical machines in the pool.
// +kubebuilder:validation:XValidation:rule="!has(self.maxNodes) || self.minNodes <= self.maxNodes",message="minNodes must be less than or equal to maxNodes"
type RunnerNodePoolScalingSpec struct {
	// MinNodes is the minimum number of physical machines to keep powered on.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinNodes int32 `json:"minNodes"`

	// MaxNodes is the maximum number of physical machines allowed to be powered on. If unset, all machines in pool are allowed.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxNodes *int32 `json:"maxNodes,omitempty"`

	// ScaleDownDelay is the duration to wait with zero demand before initiating power off.
	// +kubebuilder:default="10m"
	// +optional
	ScaleDownDelay *metav1.Duration `json:"scaleDownDelay,omitempty"`
}

// NodePoolDrainSpec defines node draining parameters for the pool.
type NodePoolDrainSpec struct {
	// Timeout is the maximum duration to wait for runner pods to drain before considering the drain stalled.
	// +kubebuilder:default="10m"
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
}

// RunnerNodePoolSpec defines the desired state of RunnerNodePool.
type RunnerNodePoolSpec struct {
	// ClusterRef references the RunnerCluster associated with this node pool.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="clusterRef is immutable"
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// Drain defines node draining parameters for machines in the pool.
	// +optional
	Drain *NodePoolDrainSpec `json:"drain,omitempty"`

	// Scaling defines scaling parameters for machines in the pool.
	// +optional
	Scaling RunnerNodePoolScalingSpec `json:"scaling,omitempty"`
}

// RunnerNodePoolStatus defines the observed state of RunnerNodePool.
type RunnerNodePoolStatus struct {
	// ObservedGeneration is the most recent generation observed for this resource.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// DesiredNodes is the number of physical machines calculated as needed to satisfy demand.
	// +optional
	DesiredNodes int32 `json:"desiredNodes"`

	// PoweredOnNodes is the number of physical machines currently observed as powered on.
	// +optional
	PoweredOnNodes int32 `json:"poweredOnNodes"`

	// ReadyNodes is the number of physical machines currently powered on and reporting Kubernetes Node Ready.
	// +optional
	ReadyNodes int32 `json:"readyNodes"`

	// ActiveRunners is the count of non-terminal runners currently tracked on this pool.
	// +optional
	ActiveRunners int32 `json:"activeRunners"`

	// PendingRunners is the count of unscheduled or pending runners waiting for compute capacity.
	// +optional
	PendingRunners int32 `json:"pendingRunners"`

	// IdleSince records the timestamp when runner demand first dropped to zero across all scale sets.
	// +optional
	IdleSince *metav1.Time `json:"idleSince,omitempty"`

	// DesiredMachines holds the planned desired state for each machine managed by this pool.
	// +optional
	DesiredMachines []MachinePlanStatus `json:"desiredMachines,omitempty"`

	// Conditions store the detailed status conditions of the node pool.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MachineDesiredState represents the target power/operational state decided by capacity planning.
// +kubebuilder:validation:Enum=Active;Off
type MachineDesiredState string

// Machine desired states.
const (
	MachineDesiredStateActive MachineDesiredState = "Active"
	MachineDesiredStateOff    MachineDesiredState = "Off"
)

// MachinePlanStatus records the planned desired state for an individual physical machine.
type MachinePlanStatus struct {
	// Name is the name of the RunnerMachine resource.
	Name string `json:"name"`

	// UID is the unique identifier of the RunnerMachine resource.
	// +optional
	UID string `json:"uid,omitempty"`

	// DesiredState is the target state decided by the capacity planner (Active or Off).
	DesiredState MachineDesiredState `json:"desiredState"`

	// IdleSince records the timestamp when this machine became idle (0 active runners and no unschedulable demand).
	// +optional
	IdleSince *metav1.Time `json:"idleSince,omitempty"`

	// DrainStartedAt records the timestamp when the machine entered draining for scale-down.
	// +optional
	DrainStartedAt *metav1.Time `json:"drainStartedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rnodepool;rnp,categories=gha;all
// +kubebuilder:printcolumn:name="Desired Nodes",type="integer",JSONPath=".status.desiredNodes",description="Desired number of physical nodes"
// +kubebuilder:printcolumn:name="Powered On",type="integer",JSONPath=".status.poweredOnNodes",description="Powered on nodes count"
// +kubebuilder:printcolumn:name="Ready Nodes",type="integer",JSONPath=".status.readyNodes",description="Ready nodes count"
// +kubebuilder:printcolumn:name="Active Runners",type="integer",JSONPath=".status.activeRunners",description="Active runners count"
// +kubebuilder:printcolumn:name="Pending Runners",type="integer",JSONPath=".status.pendingRunners",description="Pending runners count"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// RunnerNodePool is the Schema for the runnernodepools API.
type RunnerNodePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RunnerNodePoolSpec   `json:"spec,omitempty"`
	Status RunnerNodePoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RunnerNodePoolList contains a list of RunnerNodePool.
type RunnerNodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RunnerNodePool `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &RunnerNodePool{}, &RunnerNodePoolList{})
		return nil
	})
}
