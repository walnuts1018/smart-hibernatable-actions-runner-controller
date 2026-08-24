package capacity

import (
	"sort"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

type orderedCapacityPlanner struct {
	enableMultiNode bool
}

// NewOrderedCapacityPlanner creates a new Planner using the Ordered strategy.
func NewOrderedCapacityPlanner(enableMultiNode bool) Planner {
	return &orderedCapacityPlanner{
		enableMultiNode: enableMultiNode,
	}
}

func (p *orderedCapacityPlanner) Plan(machines []MachineCapacity, requiredRunners int) Plan {
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
		totalCapacity      int
		hasStartup         bool
		startupUnavailable bool
		candidates         []MachineCapacity
	)

	// Phase 1: AlwaysOn または StartupRequired マシンを優先選択
	for _, mc := range machines {
		if mc.Quarantined || mc.Maintenance {
			if mc.StartupRequired {
				startupUnavailable = true
			}
			continue
		}

		if mc.AlwaysOn || mc.StartupRequired {
			selected = append(selected, mc.Machine)
			totalCapacity += mc.Capacity
			if mc.StartupRequired {
				hasStartup = true
			}
		} else {
			candidates = append(candidates, mc)
		}
	}

	if startupUnavailable && !hasStartup {
		return Plan{
			StartupUnavailable: true,
		}
	}

	if requiredRunners <= 0 {
		// Scale to zero: only AlwaysOn / StartupRequired machines remain
		return Plan{
			SelectedMachines: selected,
			TotalCapacity:    totalCapacity,
			StartupRequired:  hasStartup,
		}
	}

	// 既に前提条件のみで必要容量を満たしている場合はそのまま返す
	if totalCapacity >= requiredRunners {
		return Plan{
			SelectedMachines:   selected,
			TotalCapacity:      totalCapacity,
			StartupRequired:    hasStartup,
			StartupUnavailable: startupUnavailable,
		}
	}

	// Phase 2: 残りのマシンを（PreviouslyDesired -> ActiveRunners降順 -> Ready -> PoweredOn -> Priority降順 -> Name昇順）でソート
	sort.SliceStable(candidates, func(i, j int) bool {
		// 0. 前回選択されていたマシンへの Stickiness 優先 (フラッピング・不要な cold start 防止)
		if candidates[i].PreviouslyDesired != candidates[j].PreviouslyDesired {
			return candidates[i].PreviouslyDesired
		}
		// 1. 実行中Runner数降順 (Bin-packing: 稼働中Runnerが多いマシンを優先残存、空きマシンから優先Drain)
		if candidates[i].ActiveRunners != candidates[j].ActiveRunners {
			return candidates[i].ActiveRunners > candidates[j].ActiveRunners
		}
		// 2. Ready状態優先
		if candidates[i].Ready != candidates[j].Ready {
			return candidates[i].Ready
		}
		// 3. PoweredOn優先
		if candidates[i].PoweredOn != candidates[j].PoweredOn {
			return candidates[i].PoweredOn
		}
		// 4. Priority降順 (大きい値ほど高優先)
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		// 5. 名前昇順で安定化
		return candidates[i].Machine.Name < candidates[j].Machine.Name
	})

	for _, mc := range candidates {
		selected = append(selected, mc.Machine)
		totalCapacity += mc.Capacity

		if totalCapacity >= requiredRunners {
			break
		}
	}

	return Plan{
		SelectedMachines: selected,
		TotalCapacity:    totalCapacity,
		StartupRequired:  hasStartup,
	}
}
