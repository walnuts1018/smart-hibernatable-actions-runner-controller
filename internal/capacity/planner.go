package capacity

import (
	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

// MachineStatus represents the status of a physical machine for autoscaling decisions.
type MachineStatus struct {
	Machine           *ghav1alpha1.RunnerMachine
	Priority          int32
	StartupRequired   bool
	AlwaysOn          bool
	PoweredOn         bool
	Ready             bool
	PowerManageable   bool
	Quarantined       bool
	Maintenance       bool
	PreviouslyDesired bool
	ActiveRunners     int
}

// Plan represents the output of autoscaling decision.
type Plan struct {
	// SelectedMachines contains the physical machines that should be DesiredState=Active.
	SelectedMachines []*ghav1alpha1.RunnerMachine

	// StartupRequired indicates whether a startup machine is included in the selection.
	StartupRequired bool

	// StartupUnavailable indicates whether the required startup machine is quarantined or unavailable.
	StartupUnavailable bool

	// MultiNodeViolated indicates if multi-node pool was encountered when MultiNode feature is disabled.
	MultiNodeViolated bool

	// PoolExhausted indicates all eligible machines in the pool are active but more capacity is needed.
	PoolExhausted bool

	// NodesStarting indicates a machine is currently powering on / starting up so we should wait.
	NodesStarting bool
}

// MachineSelector decides which machines should be powered on (Active).
type MachineSelector interface {
	Select(machines []MachineStatus, needsScaleUp bool) Plan
}
