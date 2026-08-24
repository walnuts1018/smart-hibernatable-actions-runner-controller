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

// RunnerClusterPhase represents the observed lifecycle phase of the runner Kubernetes cluster.
// +kubebuilder:validation:Enum=Offline;Starting;Ready;Degraded;Unknown
type RunnerClusterPhase string

// RunnerCluster phases.
const (
	RunnerClusterPhaseOffline  RunnerClusterPhase = "Offline"
	RunnerClusterPhaseStarting RunnerClusterPhase = "Starting"
	RunnerClusterPhaseReady    RunnerClusterPhase = "Ready"
	RunnerClusterPhaseDegraded RunnerClusterPhase = "Degraded"
	RunnerClusterPhaseUnknown  RunnerClusterPhase = "Unknown"
)

// RunnerClusterReadinessSpec defines readiness probes and timeouts for the runner cluster.
type RunnerClusterReadinessSpec struct {
	// APIRequestTimeout is the timeout for Kubernetes API requests to the runner cluster.
	// +kubebuilder:default="5s"
	// +optional
	APIRequestTimeout *metav1.Duration `json:"apiRequestTimeout,omitempty"`

	// NodeReadyTimeout is the maximum duration to wait for a physical machine's Kubernetes node to become Ready after power on.
	// +kubebuilder:default="10m"
	// +optional
	NodeReadyTimeout *metav1.Duration `json:"nodeReadyTimeout,omitempty"`
}

// RunnerClusterIdentitySpec defines identity binding expectations for cluster re-provisioning and adoption.
type RunnerClusterIdentitySpec struct {
	// ExpectedUID is the new kube-system namespace UID expected when re-adopting a re-provisioned cluster.
	// +optional
	ExpectedUID string `json:"expectedUID,omitempty"`

	// AdoptionGeneration is an incrementing counter to trigger adoption of ExpectedUID.
	// +optional
	AdoptionGeneration int64 `json:"adoptionGeneration,omitempty"`
}

// RunnerClusterSpec defines the desired state of RunnerCluster.
type RunnerClusterSpec struct {
	// KubeconfigSecretRef references the Secret containing the kubeconfig to connect to the runner Kubernetes cluster.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="kubeconfigSecretRef is immutable"
	KubeconfigSecretRef corev1.SecretKeySelector `json:"kubeconfigSecretRef"`

	// RunnerNamespace is the namespace on the runner cluster where runner Pods and Secrets are created.
	// +kubebuilder:default="gha-runners"
	// +optional
	RunnerNamespace string `json:"runnerNamespace,omitempty"`

	// Readiness defines timeouts and parameters for checking cluster readiness.
	// +optional
	Readiness RunnerClusterReadinessSpec `json:"readiness,omitempty"`

	// Identity defines identity and adoption expectations for the cluster.
	// +optional
	Identity *RunnerClusterIdentitySpec `json:"identity,omitempty"`
}

// RunnerClusterStatus defines the observed state of RunnerCluster.
type RunnerClusterStatus struct {
	// Phase is the current high-level state of the runner cluster.
	// +kubebuilder:default="Unknown"
	// +optional
	Phase RunnerClusterPhase `json:"phase,omitempty"`

	// APIReachable indicates whether the runner cluster's Kubernetes API server responded successfully to a health check.
	// +optional
	APIReachable bool `json:"apiReachable"`

	// ClusterUID is the unique identifier (kube-system namespace UID) of the remote cluster for split-brain protection.
	// +optional
	ClusterUID string `json:"clusterUID,omitempty"`

	// ObservedAdoptionGeneration records the last successfully processed AdoptionGeneration.
	// +optional
	ObservedAdoptionGeneration int64 `json:"observedAdoptionGeneration,omitempty"`

	// Conditions store the detailed status conditions of the runner cluster.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rcluster;rc,categories=gha;all
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase",description="Cluster phase"
// +kubebuilder:printcolumn:name="API Reachable",type="boolean",JSONPath=".status.apiReachable",description="Kubernetes API server reachability"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// RunnerCluster is the Schema for the runnerclusters API
type RunnerCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of RunnerCluster
	// +required
	Spec RunnerClusterSpec `json:"spec"`

	// status defines the observed state of RunnerCluster
	// +optional
	Status RunnerClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// RunnerClusterList contains a list of RunnerCluster
type RunnerClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RunnerCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &RunnerCluster{}, &RunnerClusterList{})
		return nil
	})
}
