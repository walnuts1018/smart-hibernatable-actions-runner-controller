// Package conditions defines standard status condition types and reasons.
package conditions

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Standard Condition Types
const (
	// Common
	TypeReady = "Ready"

	// RunnerCluster
	TypeKubeconfigValid = "KubeconfigValid"
	TypeAPIReachable    = "APIReachable"

	// RunnerMachine
	TypeRedfishReachable    = "RedfishReachable"
	TypePowerReady          = "PowerReady"
	TypeKubernetesNodeReady = "KubernetesNodeReady"
	TypeQuarantined         = "Quarantined"
	TypeMaintenance         = "Maintenance"
	TypeIdentityValid       = "IdentityValid"

	// RunnerNodePool
	TypeMachinesReady = "MachinesReady"
	TypeCapacityReady = "CapacityReady"
	TypeScaling       = "Scaling"
	TypeIdle          = "Idle"

	// RunnerScaleSet
	TypeGitHubReady     = "GitHubReady"
	TypeListenerReady   = "ListenerReady"
	TypeDegraded        = "Degraded"
	TypeCapacityLimited = "CapacityLimited"

	// EphemeralRunner
	TypePodReady   = "PodReady"
	TypePodCreated = "PodCreated"
)

// Standard Condition Reasons
const (
	ReasonReady                   = "Ready"
	ReasonNotReady                = "NotReady"
	ReasonSuccess                 = "Success"
	ReasonFailed                  = "Failed"
	ReasonPending                 = "Pending"
	ReasonSecretNotFound          = "SecretNotFound"
	ReasonInvalidKubeconfig       = "InvalidKubeconfig"
	ReasonAPIUnreachable          = "APIUnreachable"
	ReasonAPISucceeded            = "APISucceeded"
	ReasonPowerStateUnknown       = "PowerStateUnknown"
	ReasonPowerStateOff           = "PowerStateOff"
	ReasonPowerStateOn            = "PowerStateOn"
	ReasonPowerTransitioning      = "PowerTransitioning"
	ReasonNodeNotFound            = "NodeNotFound"
	ReasonNodeNotReady            = "NodeNotReady"
	ReasonNodeReady               = "NodeReady"
	ReasonMachinesNotReady        = "MachinesNotReady"
	ReasonMultiNodeUnsupported    = "MultiNodeUnsupported"
	ReasonScaleUp                 = "ScaleUp"
	ReasonScaleDown               = "ScaleDown"
	ReasonIdle                    = "Idle"
	ReasonActive                  = "Active"
	ReasonGitHubAuthFailed        = "GitHubAuthFailed"
	ReasonScaleSetCreated         = "ScaleSetCreated"
	ReasonScaleSetFailed          = "ScaleSetFailed"
	ReasonListenerRunning         = "ListenerRunning"
	ReasonListenerNotRunning      = "ListenerNotRunning"
	ReasonCapacitySufficient      = "CapacitySufficient"
	ReasonCapacityExceeded        = "CapacityExceeded"
	ReasonPodPending              = "PodPending"
	ReasonPodRunning              = "PodRunning"
	ReasonPodSucceeded            = "PodSucceeded"
	ReasonPodFailed               = "PodFailed"
	ReasonShutdownStalled         = "MachineShutdownStalled"
	ReasonUnsupportedRedfish      = "UnsupportedRedfish"
	ReasonListenerStateStale      = "ListenerStateStale"
	ReasonScaledToZero            = "ScaledToZero"
	ReasonCordoned                = "NodeCordoned"
	ReasonUncordoned              = "NodeUncordoned"
	ReasonDraining                = "Draining"
	ReasonExternalCordon          = "ExternalCordon"
	ReasonMaintenance             = "Maintenance"
	ReasonQuarantined             = "Quarantined"
	ReasonClusterIdentityMismatch = "ClusterIdentityMismatch"
	ReasonMachineIDMismatch       = "MachineIDMismatch"
	ReasonBootstrapUnavailable    = "BootstrapUnavailable"
)

// SetCondition adds or updates a condition in a slice of conditions.
func SetCondition(conditions *[]metav1.Condition, conditionType string, status metav1.ConditionStatus, reason, message string) {
	SetConditionWithGeneration(conditions, 0, conditionType, status, reason, message)
}

// SetConditionWithGeneration adds or updates a condition with a specific observed generation.
func SetConditionWithGeneration(conditions *[]metav1.Condition, generation int64, conditionType string, status metav1.ConditionStatus, reason, message string) {
	if conditions == nil {
		return
	}

	now := metav1.Now()
	for i, existing := range *conditions {
		if existing.Type == conditionType {
			if existing.Status != status || existing.Reason != reason || existing.Message != message || (generation != 0 && existing.ObservedGeneration != generation) {
				(*conditions)[i] = metav1.Condition{
					Type:               conditionType,
					Status:             status,
					ObservedGeneration: generation,
					LastTransitionTime: now,
					Reason:             reason,
					Message:            message,
				}
			}
			return
		}
	}

	*conditions = append(*conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
}

// IsConditionTrue checks if the given condition is True.
func IsConditionTrue(conditions []metav1.Condition, conditionType string) bool {
	for _, cond := range conditions {
		if cond.Type == conditionType {
			return cond.Status == metav1.ConditionTrue
		}
	}
	return false
}

// GetCondition returns the condition with the given type, or nil if not found.
func GetCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
