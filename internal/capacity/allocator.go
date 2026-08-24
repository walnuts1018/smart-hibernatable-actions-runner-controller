package capacity

// ScaleSetAllocationInput represents capacity requirements and constraints for a single RunnerScaleSet.
type ScaleSetAllocationInput struct {
	Name          string
	HardCommitted int32 // non-terminal active runners
	Max           int32 // Spec.Scaling.MaxRunners
}

// AllocateScaleSetCapacity distributes available physical node capacity among multiple RunnerScaleSets
// using Max-Min Fair Share, guaranteeing that sum(allocated) does not exceed physical capacity
// while preserving existing hard commitments.
func AllocateScaleSetCapacity(availableCapacity int32, inputs []ScaleSetAllocationInput) map[string]int32 {
	allocations := make(map[string]int32, len(inputs))
	if len(inputs) == 0 {
		return allocations
	}

	var totalCommitted int32
	for _, in := range inputs {
		committed := max(0, in.HardCommitted)
		if in.Max > 0 && committed > in.Max {
			committed = in.Max
		}
		allocations[in.Name] = committed
		totalCommitted += committed
	}

	remaining := availableCapacity - totalCommitted
	if remaining <= 0 {
		return allocations
	}

	// Iterative water-filling Max-Min fair share distribution of remaining capacity
	for remaining > 0 {
		var eligible []*ScaleSetAllocationInput
		for i := range inputs {
			in := &inputs[i]
			current := allocations[in.Name]
			if in.Max <= 0 || current < in.Max {
				eligible = append(eligible, in)
			}
		}

		if len(eligible) == 0 {
			break
		}

		fairShare := remaining / int32(len(eligible))
		if fairShare == 0 {
			// If remaining is smaller than eligible count, distribute 1 to each until exhausted
			for _, in := range eligible {
				if remaining <= 0 {
					break
				}
				allocations[in.Name]++
				remaining--
			}
			break
		}

		distributedInRound := int32(0)
		for _, in := range eligible {
			current := allocations[in.Name]
			delta := fairShare
			if in.Max > 0 && current+delta > in.Max {
				delta = in.Max - current
			}
			if delta > 0 {
				allocations[in.Name] += delta
				remaining -= delta
				distributedInRound += delta
			}
		}

		if distributedInRound == 0 {
			break
		}
	}

	return allocations
}
