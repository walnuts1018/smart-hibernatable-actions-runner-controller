package capacity

import (
	"reflect"
	"testing"
)

func TestAllocateScaleSetCapacity(t *testing.T) {
	tests := []struct {
		name              string
		availableCapacity int32
		inputs            []ScaleSetAllocationInput
		expected          map[string]int32
	}{
		{
			name:              "empty inputs",
			availableCapacity: 10,
			inputs:            nil,
			expected:          map[string]int32{},
		},
		{
			name:              "single scaleset with sufficient capacity",
			availableCapacity: 10,
			inputs: []ScaleSetAllocationInput{
				{Name: "ss1", HardCommitted: 2, Max: 5},
			},
			expected: map[string]int32{"ss1": 5},
		},
		{
			name:              "equal fair share with excess capacity",
			availableCapacity: 16,
			inputs: []ScaleSetAllocationInput{
				{Name: "ss1", HardCommitted: 0, Max: 10},
				{Name: "ss2", HardCommitted: 0, Max: 10},
			},
			expected: map[string]int32{"ss1": 8, "ss2": 8},
		},
		{
			name:              "fair share capped by max runners",
			availableCapacity: 16,
			inputs: []ScaleSetAllocationInput{
				{Name: "ss1", HardCommitted: 0, Max: 4},
				{Name: "ss2", HardCommitted: 0, Max: 20},
			},
			expected: map[string]int32{"ss1": 4, "ss2": 12},
		},
		{
			name:              "hard commitments preserved when capacity is tight",
			availableCapacity: 8,
			inputs: []ScaleSetAllocationInput{
				{Name: "ss1", HardCommitted: 6, Max: 10},
				{Name: "ss2", HardCommitted: 2, Max: 10},
			},
			expected: map[string]int32{"ss1": 6, "ss2": 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AllocateScaleSetCapacity(tt.availableCapacity, tt.inputs)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("AllocateScaleSetCapacity() = %v, want %v", result, tt.expected)
			}
		})
	}
}
