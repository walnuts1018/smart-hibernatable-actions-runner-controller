package capacity

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

func TestOrderedCapacityPlanner_Plan(t *testing.T) {
	tests := []struct {
		name            string
		enableMultiNode bool
		machines        []MachineCapacity
		requiredRunners int
		wantSelected    []string
		wantTotalCap    int
		wantViolated    bool
	}{
		{
			name:            "scale to zero",
			enableMultiNode: false,
			machines: []MachineCapacity{
				{
					Machine:   &ghav1alpha1.RunnerMachine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					Capacity:  2,
					Priority:  100,
					Bootstrap: true,
					PoweredOn: true,
				},
			},
			requiredRunners: 0,
			wantSelected:    []string{},
			wantTotalCap:    0,
			wantViolated:    false,
		},
		{
			name:            "single machine scale from zero",
			enableMultiNode: false,
			machines: []MachineCapacity{
				{
					Machine:   &ghav1alpha1.RunnerMachine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					Capacity:  2,
					Priority:  100,
					Bootstrap: true,
					PoweredOn: false,
				},
			},
			requiredRunners: 2,
			wantSelected:    []string{"m1"},
			wantTotalCap:    2,
			wantViolated:    false,
		},
		{
			name:            "multi-node unsupported violation in v1",
			enableMultiNode: false,
			machines: []MachineCapacity{
				{
					Machine:   &ghav1alpha1.RunnerMachine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					Capacity:  2,
					Priority:  100,
					Bootstrap: true,
				},
				{
					Machine:   &ghav1alpha1.RunnerMachine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					Capacity:  2,
					Priority:  200,
					Bootstrap: false,
				},
			},
			requiredRunners: 2,
			wantViolated:    true,
		},
		{
			name:            "multi-node enabled ordered selection",
			enableMultiNode: true,
			machines: []MachineCapacity{
				{
					Machine:   &ghav1alpha1.RunnerMachine{ObjectMeta: metav1.ObjectMeta{Name: "worker2"}},
					Capacity:  4,
					Priority:  300,
					Bootstrap: false,
				},
				{
					Machine:   &ghav1alpha1.RunnerMachine{ObjectMeta: metav1.ObjectMeta{Name: "bootstrap"}},
					Capacity:  2,
					Priority:  100,
					Bootstrap: true,
				},
				{
					Machine:   &ghav1alpha1.RunnerMachine{ObjectMeta: metav1.ObjectMeta{Name: "worker1"}},
					Capacity:  2,
					Priority:  200,
					Bootstrap: false,
				},
			},
			requiredRunners: 3,
			wantSelected:    []string{"bootstrap", "worker1"},
			wantTotalCap:    4,
			wantViolated:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planner := NewOrderedCapacityPlanner(tt.enableMultiNode)
			plan := planner.Plan(tt.machines, tt.requiredRunners)

			if plan.MultiNodeViolated != tt.wantViolated {
				t.Fatalf("expected MultiNodeViolated=%v, got %v", tt.wantViolated, plan.MultiNodeViolated)
			}

			if tt.wantViolated {
				return
			}

			if plan.TotalCapacity != tt.wantTotalCap {
				t.Errorf("expected TotalCapacity=%d, got %d", tt.wantTotalCap, plan.TotalCapacity)
			}

			if len(plan.SelectedMachines) != len(tt.wantSelected) {
				t.Fatalf("expected %d selected machines, got %d", len(tt.wantSelected), len(plan.SelectedMachines))
			}

			for i, name := range tt.wantSelected {
				if plan.SelectedMachines[i].Name != name {
					t.Errorf("selected[%d]: expected %q, got %q", i, name, plan.SelectedMachines[i].Name)
				}
			}
		})
	}
}
