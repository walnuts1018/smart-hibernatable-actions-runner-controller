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

// Machine power states.
const (
	PowerStateUnknown     PowerState = "Unknown"
	PowerStateOff         PowerState = "Off"
	PowerStatePoweringOn  PowerState = "PoweringOn"
	PowerStateOn          PowerState = "On"
	PowerStatePoweringOff PowerState = "PoweringOff"
)

// RunnerMachinePowerPolicy defines the power management policy for a machine.
// +kubebuilder:validation:Enum=OnDemand;AlwaysOn
type RunnerMachinePowerPolicy string

const (
	// RunnerMachinePowerPolicyOnDemand allows SHARC to power off the machine when idle.
	RunnerMachinePowerPolicyOnDemand RunnerMachinePowerPolicy = "OnDemand"
	// RunnerMachinePowerPolicyAlwaysOn prevents SHARC from powering off the machine during scale-down.
	RunnerMachinePowerPolicyAlwaysOn RunnerMachinePowerPolicy = "AlwaysOn"
)

// RedfishTimeoutPolicy defines what action to take when graceful shutdown times out.
// +kubebuilder:validation:Enum=Abort;ForceOff
type RedfishTimeoutPolicy string

const (
	// RedfishTimeoutPolicyAbort aborts the power off operation and keeps the machine running.
	RedfishTimeoutPolicyAbort RedfishTimeoutPolicy = "Abort"
	// RedfishTimeoutPolicyForceOff initiates an immediate hard power cut (ForceOff) after timeout.
	RedfishTimeoutPolicyForceOff RedfishTimeoutPolicy = "ForceOff"
)

// RunnerMachineCapacity defines the capacity provided by the physical machine.
type RunnerMachineCapacity struct {
	// RunnerSlots is the maximum number of concurrent runner slots this machine provides for capacity planning.
	// +kubebuilder:validation:Minimum=1
	RunnerSlots int32 `json:"runnerSlots"`
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

// RedfishShutdownSpec defines timeout and fallback settings for Redfish power shutdown.
type RedfishShutdownSpec struct {
	// Timeout is the duration to wait for graceful OS shutdown before applying the TimeoutPolicy.
	// +kubebuilder:default="3m"
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// TimeoutPolicy specifies whether to abort or force off if graceful shutdown exceeds the timeout.
	// +kubebuilder:default="Abort"
	// +optional
	TimeoutPolicy RedfishTimeoutPolicy `json:"timeoutPolicy,omitempty"`
}

// RedfishPowerSpec defines Redfish power control options.
type RedfishPowerSpec struct {
	// Shutdown defines parameters for graceful shutdown and timeout fallback.
	// +optional
	Shutdown RedfishShutdownSpec `json:"shutdown,omitempty"`
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

// MachineDrainSpec defines node draining parameters for the machine.
type MachineDrainSpec struct {
	// Timeout is the maximum duration to wait for runner pods to drain before considering the drain stalled.
	// +kubebuilder:default="10m"
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
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

	// BoundMachineID is the machine-id from Node.Status.NodeInfo.MachineID when SHARC initially bound this RunnerMachine.
	// +optional
	BoundMachineID string `json:"boundMachineID,omitempty"`

	// ObservedMachineID is the current machine-id observed from Node.Status.NodeInfo.MachineID.
	// +optional
	ObservedMachineID string `json:"observedMachineID,omitempty"`

	// MachineID is preserved for backward compatibility and matches BoundMachineID.
	// +optional
	MachineID string `json:"machineID,omitempty"`
}

// RedfishCircuitState defines the circuit breaker state for BMC communication.
// +kubebuilder:validation:Enum=Closed;Open;HalfOpen
type RedfishCircuitState string

const (
	// RedfishCircuitClosed indicates normal BMC communication.
	RedfishCircuitClosed RedfishCircuitState = "Closed"
	// RedfishCircuitOpen indicates BMC communication is paused due to repeated failures.
	RedfishCircuitOpen RedfishCircuitState = "Open"
	// RedfishCircuitHalfOpen indicates a single probe attempt is permitted to test BMC recovery.
	RedfishCircuitHalfOpen RedfishCircuitState = "HalfOpen"
)

// RedfishHealthStatus records circuit breaker and error backoff state for BMC communication.
type RedfishHealthStatus struct {
	// Circuit is the current circuit breaker state.
	// +kubebuilder:default="Closed"
	// +optional
	Circuit RedfishCircuitState `json:"circuit,omitempty"`

	// ConsecutiveFailures is the number of consecutive Redfish communication failures.
	// +optional
	ConsecutiveFailures int32 `json:"consecutiveFailures,omitempty"`

	// LastSuccessTime is the timestamp of the last successful Redfish communication.
	// +optional
	LastSuccessTime *metav1.Time `json:"lastSuccessTime,omitempty"`

	// LastFailureTime is the timestamp of the last failed Redfish communication.
	// +optional
	LastFailureTime *metav1.Time `json:"lastFailureTime,omitempty"`

	// NextProbeTime is the timestamp when the next Redfish probe is allowed after backoff.
	// +optional
	NextProbeTime *metav1.Time `json:"nextProbeTime,omitempty"`
}

// MaintenancePowerPolicy defines power handling during maintenance mode.
// +kubebuilder:validation:Enum=Preserve;PowerOff
type MaintenancePowerPolicy string

const (
	// MaintenancePowerPolicyPreserve keeps the machine powered on during maintenance.
	MaintenancePowerPolicyPreserve MaintenancePowerPolicy = "Preserve"
	// MaintenancePowerPolicyPowerOff powers off the machine after draining during maintenance.
	MaintenancePowerPolicyPowerOff MaintenancePowerPolicy = "PowerOff"
)

// MachineMaintenanceSpec defines maintenance mode settings.
type MachineMaintenanceSpec struct {
	// Enabled indicates whether the machine is in maintenance mode.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// PowerPolicy defines whether to preserve power or power off after draining during maintenance.
	// +kubebuilder:default="Preserve"
	// +optional
	PowerPolicy MaintenancePowerPolicy `json:"powerPolicy,omitempty"`
}

// MachineQuarantineStatus records quarantine state when a machine repeatedly fails health/node checks.
type MachineQuarantineStatus struct {
	// Reason describes why the machine was quarantined.
	Reason string `json:"reason"`

	// Since is the timestamp when the machine entered quarantine.
	Since metav1.Time `json:"since"`

	// ConsecutiveFailures is the number of consecutive readiness or operation failures.
	// +kubebuilder:default=1
	ConsecutiveFailures int32 `json:"consecutiveFailures"`

	// HealthySince is the timestamp when the machine started observing continuous healthy Node state.
	// +optional
	HealthySince *metav1.Time `json:"healthySince,omitempty"`
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

	// DrainVerifiedAt is the timestamp when the node was verified to have 0 active runner pods before initiating shutdown.
	// +optional
	DrainVerifiedAt *metav1.Time `json:"drainVerifiedAt,omitempty"`

	// Attempts is the number of times the command has been dispatched.
	// +kubebuilder:default=1
	Attempts int32 `json:"attempts"`
}

// RunnerMachineSpec defines the desired state of RunnerMachine.
type RunnerMachineSpec struct {
	// ClusterRef references the RunnerCluster this machine belongs to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="clusterRef is immutable"
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// NodePoolRef references the RunnerNodePool this machine belongs to.
	// +optional
	NodePoolRef *corev1.LocalObjectReference `json:"nodePoolRef,omitempty"`

	// NodeName is the expected Node name in the runner Kubernetes cluster.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodeName is immutable"
	NodeName string `json:"nodeName"`

	// PowerPolicy defines the power policy (OnDemand or AlwaysOn).
	// +kubebuilder:default="OnDemand"
	// +optional
	PowerPolicy RunnerMachinePowerPolicy `json:"powerPolicy,omitempty"`

	// Capacity specifies the runner capacity provided by this machine.
	// +kubebuilder:validation:Required
	Capacity RunnerMachineCapacity `json:"capacity"`

	// Priority specifies selection priority when scaling up (higher value = higher priority).
	// +kubebuilder:default=0
	// +optional
	Priority int32 `json:"priority,omitempty"`

	// Redfish specifies Redfish BMC configuration for power management.
	// +kubebuilder:validation:Required
	Redfish RedfishSpec `json:"redfish"`

	// Drain defines node draining parameters for the machine.
	// +optional
	Drain *MachineDrainSpec `json:"drain,omitempty"`

	// Maintenance enables maintenance mode, preventing SHARC from uncordoning or powering off this machine.
	// +optional
	Maintenance *MachineMaintenanceSpec `json:"maintenance,omitempty"`
}

// RunnerMachineStatus defines the observed state of RunnerMachine.
type RunnerMachineStatus struct {
	// ObservedGeneration is the most recent generation observed for this resource.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// PowerState is the observed Redfish power state of the machine.
	// +kubebuilder:default="Unknown"
	// +optional
	PowerState PowerState `json:"powerState,omitempty"`

	// Operation tracks an in-progress power transition to prevent redundant API calls.
	// +optional
	Operation *PowerOperationStatus `json:"operation,omitempty"`

	// RedfishHealth tracks BMC communication health, error backoff, and circuit breaker state.
	// +optional
	RedfishHealth *RedfishHealthStatus `json:"redfishHealth,omitempty"`

	// Kubernetes reflects the state of the machine's Node object on the runner cluster.
	// +optional
	Kubernetes RunnerMachineKubernetesStatus `json:"kubernetes,omitempty"`

	// ExternallyCordoned indicates whether the Node was cordoned by an external actor (e.g. admin kubectl cordon).
	// +optional
	ExternallyCordoned bool `json:"externallyCordoned"`

	// Quarantine records isolation state if the machine failed node readiness or shutdown timeout.
	// +optional
	Quarantine *MachineQuarantineStatus `json:"quarantine,omitempty"`

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
// +kubebuilder:printcolumn:name="Runner Slots",type="integer",JSONPath=".spec.capacity.runnerSlots",description="Declared runner slots capacity"
// +kubebuilder:printcolumn:name="Priority",type="integer",JSONPath=".spec.priority",description="Scale-up priority"
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
