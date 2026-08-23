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

// PowerState represents the physical power state of a machine.
// +kubebuilder:validation:Enum=Unknown;Off;PoweringOn;On;PoweringOff
type PowerState string

const (
	PowerStateUnknown     PowerState = "Unknown"
	PowerStateOff         PowerState = "Off"
	PowerStatePoweringOn  PowerState = "PoweringOn"
	PowerStateOn          PowerState = "On"
	PowerStatePoweringOff PowerState = "PoweringOff"
)

// RunnerMachineCapacity defines the capacity provided by the physical machine.
type RunnerMachineCapacity struct {
	// Runners is the maximum number of concurrent runner Pods this machine can host.
	// +kubebuilder:validation:Minimum=1
	Runners int32 `json:"runners"`
}

// RedfishTLSSpec defines TLS configuration for connecting to the Redfish BMC endpoint.
type RedfishTLSSpec struct {
	// CASecretRef optionally references a Secret containing the CA certificate to trust.
	// +optional
	CASecretRef *corev1.SecretKeySelector `json:"caSecretRef,omitempty"`

	// InsecureSkipVerify skips TLS certificate verification when connecting to the Redfish endpoint.
	// +kubebuilder:default=false
	// +optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// RedfishPowerSpec defines timeout and fallback settings for Redfish power operations.
type RedfishPowerSpec struct {
	// ShutdownTimeout is the duration to wait for graceful shutdown before considering the operation stalled.
	// +kubebuilder:default="3m"
	// +optional
	ShutdownTimeout *metav1.Duration `json:"shutdownTimeout,omitempty"`

	// ForceOffAfter is the duration after which ForceOff fallback is allowed if graceful shutdown fails. 0s disables ForceOff fallback.
	// +kubebuilder:default="0s"
	// +optional
	ForceOffAfter *metav1.Duration `json:"forceOffAfter,omitempty"`
}

// RedfishSpec defines parameters for Redfish out-of-band management.
// +kubebuilder:validation:XValidation:rule="self.endpoint.startsWith('http://') || self.endpoint.startsWith('https://')",message="endpoint must start with http:// or https://"
type RedfishSpec struct {
	// Endpoint is the URL of the Redfish service (e.g., https://192.168.10.50).
	// +kubebuilder:validation:Required
	Endpoint string `json:"endpoint"`

	// SystemID is the Redfish ComputerSystem ID to control (e.g., "1").
	// +kubebuilder:default="1"
	// +optional
	SystemID string `json:"systemID,omitempty"`

	// CredentialsSecretRef references the Secret containing Redfish username and password keys.
	// +kubebuilder:validation:Required
	CredentialsSecretRef corev1.LocalObjectReference `json:"credentialsSecretRef"`

	// TLS defines TLS parameters for Redfish connections.
	// +optional
	TLS RedfishTLSSpec `json:"tls,omitempty"`

	// Power defines power control options.
	// +optional
	Power RedfishPowerSpec `json:"power,omitempty"`
}

// RunnerMachineKubernetesStatus defines the observed Kubernetes Node status on the runner cluster.
type RunnerMachineKubernetesStatus struct {
	// Present indicates whether a corresponding Kubernetes Node object exists on the runner cluster.
	// +optional
	Present bool `json:"present"`

	// Ready indicates whether the corresponding Kubernetes Node has Ready condition=True.
	// +optional
	Ready bool `json:"ready"`

	// NodeUID is the UID of the Node object on the runner cluster.
	// +optional
	NodeUID string `json:"nodeUID,omitempty"`

	// MachineID is the machine-id from Node.Status.NodeInfo.MachineID on the runner cluster.
	// +optional
	MachineID string `json:"machineID,omitempty"`
}

// RunnerMachineSpec defines the desired state of RunnerMachine.
type RunnerMachineSpec struct {
	// ClusterRef references the RunnerCluster this machine belongs to.
	// +kubebuilder:validation:Required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// KubernetesNodeName is the expected Node name in the runner Kubernetes cluster.
	// +kubebuilder:validation:Required
	KubernetesNodeName string `json:"kubernetesNodeName"`

	// Bootstrap indicates whether this machine is the bootstrap node required for the runner cluster Kubernetes API to be available.
	// +kubebuilder:default=false
	// +optional
	Bootstrap bool `json:"bootstrap,omitempty"`

	// Capacity specifies the runner capacity provided by this machine.
	// +kubebuilder:validation:Required
	Capacity RunnerMachineCapacity `json:"capacity"`

	// Priority specifies selection priority when scaling up (lower value = higher priority).
	// +kubebuilder:default=100
	// +optional
	Priority int32 `json:"priority,omitempty"`

	// Redfish specifies Redfish BMC configuration for power management.
	// +kubebuilder:validation:Required
	Redfish RedfishSpec `json:"redfish"`
}

// PowerOperationType represents the type of power action in progress.
// +kubebuilder:validation:Enum=PowerOn;GracefulShutdown;ForceOff
type PowerOperationType string

const (
	// PowerOperationTypePowerOn indicates a PowerOn operation is underway.
	PowerOperationTypePowerOn PowerOperationType = "PowerOn"
	// PowerOperationTypeGracefulShutdown indicates a GracefulShutdown operation is underway.
	PowerOperationTypeGracefulShutdown PowerOperationType = "GracefulShutdown"
	// PowerOperationTypeForceOff indicates a ForceOff operation is underway.
	PowerOperationTypeForceOff PowerOperationType = "ForceOff"
)

// PowerOperationStatus records details of an ongoing or recent power operation.
type PowerOperationStatus struct {
	// Type is the kind of power operation being performed.
	Type PowerOperationType `json:"type"`

	// StartedAt is the timestamp when this operation was initiated.
	StartedAt metav1.Time `json:"startedAt"`

	// LastAttemptAt is the timestamp when the operation command was last sent to Redfish.
	LastAttemptAt metav1.Time `json:"lastAttemptAt"`

	// Attempts is the number of times the command has been dispatched.
	// +kubebuilder:default=1
	Attempts int32 `json:"attempts"`
}

// RunnerMachineStatus defines the observed state of RunnerMachine.
type RunnerMachineStatus struct {
	// PowerState is the observed Redfish power state of the machine.
	// +kubebuilder:default="Unknown"
	// +optional
	PowerState PowerState `json:"powerState,omitempty"`

	// Operation tracks an in-progress power transition to prevent redundant API calls.
	// +optional
	Operation *PowerOperationStatus `json:"operation,omitempty"`

	// Kubernetes reflects the state of the machine's Node object on the runner cluster.
	// +optional
	Kubernetes RunnerMachineKubernetesStatus `json:"kubernetes,omitempty"`

	// LastPowerTransitionTime is the timestamp of the last recorded power state transition.
	// +optional
	LastPowerTransitionTime *metav1.Time `json:"lastPowerTransitionTime,omitempty"`

	// Conditions store the detailed status conditions of the machine.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rmachine;rm,categories=gha;all
// +kubebuilder:printcolumn:name="Power State",type="string",JSONPath=".status.powerState",description="Observed power state"
// +kubebuilder:printcolumn:name="Node Ready",type="boolean",JSONPath=".status.kubernetes.ready",description="Kubernetes Node readiness"
// +kubebuilder:printcolumn:name="Runners Capacity",type="integer",JSONPath=".spec.capacity.runners",description="Declared runner capacity"
// +kubebuilder:printcolumn:name="Bootstrap",type="boolean",JSONPath=".spec.bootstrap",description="Bootstrap machine flag"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// RunnerMachine is the Schema for the runnermachines API.
type RunnerMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RunnerMachineSpec   `json:"spec,omitempty"`
	Status RunnerMachineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RunnerMachineList contains a list of RunnerMachine.
type RunnerMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RunnerMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &RunnerMachine{}, &RunnerMachineList{})
		return nil
	})
}
