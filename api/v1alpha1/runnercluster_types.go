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

// RunnerClusterStartupSpec defines machines required to bootstrap/start the remote cluster.
type RunnerClusterStartupSpec struct {
	// MachineRefs lists the RunnerMachines required to bring up the remote cluster control plane / API.
	// +optional
	MachineRefs []corev1.LocalObjectReference `json:"machineRefs,omitempty"`
}

// RunnerClusterIdentitySpec defines identity binding expectations for cluster re-provisioning and adoption.
type RunnerClusterIdentitySpec struct {
	// ExpectedClusterUID is the new kube-system namespace UID expected when adopting a re-provisioned cluster.
	// +optional
	ExpectedClusterUID string `json:"expectedClusterUID,omitempty"`
}

// RunnerClusterSpec defines the desired state of RunnerCluster.
type RunnerClusterSpec struct {
	// KubeconfigSecretRef references the Secret containing the kubeconfig to connect to the runner Kubernetes cluster.
	// +kubebuilder:validation:Required
	KubeconfigSecretRef corev1.SecretKeySelector `json:"kubeconfigSecretRef"`

	// RunnerNamespace is the namespace on the runner cluster where runner Pods and Secrets are created.
	// +kubebuilder:default="gha-runners"
	// +optional
	RunnerNamespace string `json:"runnerNamespace,omitempty"`

	// Startup defines machines that must be powered on to start the remote cluster API.
	// +optional
	Startup *RunnerClusterStartupSpec `json:"startup,omitempty"`

	// Readiness defines timeouts and parameters for checking cluster readiness.
	// +optional
	Readiness RunnerClusterReadinessSpec `json:"readiness,omitempty"`

	// Identity defines identity and adoption expectations for the cluster.
	// +optional
	Identity *RunnerClusterIdentitySpec `json:"identity,omitempty"`
}

// RunnerClusterStatus defines the observed state of RunnerCluster.
type RunnerClusterStatus struct {
	// ObservedGeneration is the most recent generation observed for this resource.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

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
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RunnerClusterSpec   `json:"spec,omitempty"`
	Status RunnerClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RunnerClusterList contains a list of RunnerCluster
type RunnerClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RunnerCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &RunnerCluster{}, &RunnerClusterList{})
		return nil
	})
}
