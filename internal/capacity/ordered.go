package capacity

import (
	"sort"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

type orderedMachineSelector struct {
	enableMultiNode bool
}

// NewOrderedMachineSelector creates a new MachineSelector using the Ordered strategy.
func NewOrderedMachineSelector(enableMultiNode bool) MachineSelector {
	return &orderedMachineSelector{
		enableMultiNode: enableMultiNode,
	}
}

// NewOrderedCapacityPlanner creates a new MachineSelector (backward compatibility alias).
func NewOrderedCapacityPlanner(enableMultiNode bool) MachineSelector {
	return NewOrderedMachineSelector(enableMultiNode)
}

func (p *orderedMachineSelector) Select(machines []MachineStatus, needsScaleUp bool) Plan {
	if len(machines) == 0 {
		return Plan{}
	}

	// In v1, if MultiNode is disabled and multiple machines exist in the pool, flag violation
	if !p.enableMultiNode && len(machines) > 1 {
		return Plan{
			MultiNodeViolated: true,
		}
	}

	var (
		selected           []*ghav1alpha1.RunnerMachine
		hasStartup         bool
		startupUnavailable bool
		hasStarting        bool
		candidates         []MachineStatus
	)

	// Phase 1: AlwaysOn or Already Active machines
	for _, mc := range machines {
		if mc.Quarantined || mc.Maintenance {
			if mc.StartupRequired {
				startupUnavailable = true
			}
			continue
		}

		if mc.StartupRequired {
			hasStartup = true
		}

		if mc.AlwaysOn || mc.PreviouslyDesired {
			selected = append(selected, mc.Machine)
			if mc.Machine.Status.PowerState != ghav1alpha1.PowerStateOn || !mc.Ready {
				hasStarting = true
			}
		} else {
			// Inactive candidate
			candidates = append(candidates, mc)
		}
	}

	if startupUnavailable && !hasStartup {
		return Plan{
			StartupUnavailable: true,
		}
	}

	// If no scale-up needed, return the currently active/mandatory machines
	if !needsScaleUp {
		return Plan{
			SelectedMachines: selected,
			StartupRequired:  hasStartup,
		}
	}

	// If a machine is already starting up, wait for it before starting more (1-machine-at-a-time feedback loop)
	if hasStarting {
		return Plan{
			SelectedMachines: selected,
			StartupRequired:  hasStartup,
			NodesStarting:    true,
		}
	}

	// If we need scale-up and no machines are currently starting, select ONE candidate (StartupRequired first, then Priority desc, Name asc)
	if len(candidates) > 0 {
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].StartupRequired != candidates[j].StartupRequired {
				return candidates[i].StartupRequired
			}
			if candidates[i].Priority != candidates[j].Priority {
				return candidates[i].Priority > candidates[j].Priority
			}
			return candidates[i].Machine.Name < candidates[j].Machine.Name
		})

		// Select exactly one candidate to power on
		selected = append(selected, candidates[0].Machine)
		return Plan{
			SelectedMachines: selected,
			StartupRequired:  hasStartup,
			NodesStarting:    true,
		}
	}

	// All available machines in pool are already selected and active, but more capacity is needed
	return Plan{
		SelectedMachines: selected,
		StartupRequired:  hasStartup,
		PoolExhausted:    true,
	}
}
