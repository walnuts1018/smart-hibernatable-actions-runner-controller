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

// EphemeralRunnerSetSpec defines the desired state of EphemeralRunnerSet.
type EphemeralRunnerSetSpec struct {
	// ScaleSetRef references the parent RunnerScaleSet.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="scaleSetRef is immutable"
	ScaleSetRef corev1.LocalObjectReference `json:"scaleSetRef"`

	// Replicas is the desired number of ephemeral runners to maintain based on GitHub demand.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Runner defines the runner pod template and container settings.
	// +kubebuilder:validation:Required
	Runner RunnerTemplateSpec `json:"runner"`
}

// EphemeralRunnerSetStatus defines the observed state of EphemeralRunnerSet.
type EphemeralRunnerSetStatus struct {
	// ObservedGeneration is the most recent generation observed for this resource.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Replicas is the most recently observed desired replica count.
	// +optional
	Replicas int32 `json:"replicas"`

	// ActiveReplicas is the count of non-terminal EphemeralRunner custom resources currently tracked.
	// +optional
	ActiveReplicas int32 `json:"activeReplicas"`

	// Conditions store the detailed status conditions of the runner set.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ers;runnerset,categories=sharc;all
// +kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".spec.replicas",description="Desired replicas"
// +kubebuilder:printcolumn:name="Active",type="integer",JSONPath=".status.activeReplicas",description="Active runners count"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// EphemeralRunnerSet is the Schema for the ephemeralrunnersets API.
type EphemeralRunnerSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EphemeralRunnerSetSpec   `json:"spec,omitempty"`
	Status EphemeralRunnerSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EphemeralRunnerSetList contains a list of EphemeralRunnerSet.
type EphemeralRunnerSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EphemeralRunnerSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &EphemeralRunnerSet{}, &EphemeralRunnerSetList{})
		return nil
	})
}
