package capacity

import (
	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

// MachineCapacity represents capacity and current status of a physical machine.
type MachineCapacity struct {
	Machine           *ghav1alpha1.RunnerMachine
	Capacity          int
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

// Plan represents the output of capacity planning.
type Plan struct {
	// SelectedMachines contains the physical machines that should be powered on.
	SelectedMachines []*ghav1alpha1.RunnerMachine

	// TotalCapacity is the sum of runner capacities from selected machines.
	TotalCapacity int

	// StartupRequired indicates whether a startup machine is included in the selection.
	StartupRequired bool

	// StartupUnavailable indicates whether the required startup machine is quarantined or unavailable.
	StartupUnavailable bool

	// MultiNodeViolated indicates if multi-node pool was encountered when MultiNode feature is disabled.
	MultiNodeViolated bool
}

// Planner calculates which machines should be powered on to satisfy demanded runner capacity.
type Planner interface {
	Plan(machines []MachineCapacity, requiredRunners int) Plan
}
