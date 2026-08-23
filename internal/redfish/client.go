package redfish

import (
	"context"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

// PowerController defines operations for controlling and observing physical machine power state.
type PowerController interface {
	// GetPowerState returns the observed power state of the machine.
	GetPowerState(ctx context.Context) (ghav1alpha1.PowerState, error)

	// PowerOn powers on the physical machine.
	PowerOn(ctx context.Context) error

	// GracefulShutdown initiates a graceful OS shutdown via ACPI power button or GracefulShutdown reset.
	GracefulShutdown(ctx context.Context) error

	// ForceOff immediately cuts power to the machine.
	ForceOff(ctx context.Context) error

	// ValidateSupport checks Redfish compatibility and reachability.
	ValidateSupport(ctx context.Context) error
}

// PowerControllerFactory creates PowerController instances.
type PowerControllerFactory interface {
	NewController(spec ghav1alpha1.RedfishSpec, username, password string, caCert []byte) (PowerController, error)
}
