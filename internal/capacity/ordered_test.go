package capacity

import (
	"testing"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

func TestOrderedMachineSelector_Select(t *testing.T) {
	tests := []struct {
		name            string
		enableMultiNode bool
		machines        []MachineStatus
		needsScaleUp    bool
		wantSelected    []string
		wantViolated    bool
		wantStarting    bool
		wantExhausted   bool
	}{
		{
			name:            "scale to zero - no scale up needed",
			enableMultiNode: false,
			machines: []MachineStatus{
				{
					Machine:         &ghav1alpha1.RunnerMachine{Name: "m1", Status: ghav1alpha1.RunnerMachineStatus{PowerState: ghav1alpha1.PowerStateOn}},
					Priority:        100,
					StartupRequired: true,
					PoweredOn:       true,
					Ready:           true,
				},
			},
			needsScaleUp: false,
			wantSelected: []string{"m1"}, // startup required / always on stays
			wantViolated: false,
		},
		{
			name:            "single machine scale from zero without startup",
			enableMultiNode: false,
			machines: []MachineStatus{
				{
					Machine:   &ghav1alpha1.RunnerMachine{Name: "m1", Status: ghav1alpha1.RunnerMachineStatus{PowerState: ghav1alpha1.PowerStateOff}},
					Priority:  100,
					PoweredOn: false,
				},
			},
			needsScaleUp: true,
			wantSelected: []string{"m1"},
			wantStarting: true,
			wantViolated: false,
		},
		{
			name:            "multi-node disabled violation when multiple machines present",
			enableMultiNode: false,
			machines: []MachineStatus{
				{
					Machine:  &ghav1alpha1.RunnerMachine{Name: "m1"},
					Priority: 200,
				},
				{
					Machine:  &ghav1alpha1.RunnerMachine{Name: "m2"},
					Priority: 100,
				},
			},
			needsScaleUp: true,
			wantViolated: true,
		},
		{
			name:            "multi-node enabled ordered selection - picks one candidate at a time (higher priority first)",
			enableMultiNode: true,
			machines: []MachineStatus{
				{
					Machine:           &ghav1alpha1.RunnerMachine{Name: "startup-node", Status: ghav1alpha1.RunnerMachineStatus{PowerState: ghav1alpha1.PowerStateOn}},
					Priority:          100,
					StartupRequired:   true,
					Ready:             true,
					PreviouslyDesired: true,
				},
				{
					Machine:         &ghav1alpha1.RunnerMachine{Name: "worker2", Status: ghav1alpha1.RunnerMachineStatus{PowerState: ghav1alpha1.PowerStateOff}},
					Priority:        100, // lower priority
					StartupRequired: false,
				},
				{
					Machine:         &ghav1alpha1.RunnerMachine{Name: "worker1", Status: ghav1alpha1.RunnerMachineStatus{PowerState: ghav1alpha1.PowerStateOff}},
					Priority:        200, // higher priority
					StartupRequired: false,
				},
			},
			needsScaleUp: true,
			wantSelected: []string{"startup-node", "worker1"},
			wantStarting: true,
			wantViolated: false,
		},
		{
			name:            "starting machine in progress prevents starting another candidate",
			enableMultiNode: true,
			machines: []MachineStatus{
				{
					Machine:           &ghav1alpha1.RunnerMachine{Name: "worker1", Status: ghav1alpha1.RunnerMachineStatus{PowerState: ghav1alpha1.PowerStatePoweringOn}},
					Priority:          200,
					PreviouslyDesired: true,
					Ready:             false, // starting up
				},
				{
					Machine:   &ghav1alpha1.RunnerMachine{Name: "worker2", Status: ghav1alpha1.RunnerMachineStatus{PowerState: ghav1alpha1.PowerStateOff}},
					Priority:  100,
					PoweredOn: false,
				},
			},
			needsScaleUp: true,
			wantSelected: []string{"worker1"},
			wantStarting: true, // waits for worker1
		},
		{
			name:            "pool exhausted when all machines are active and scale-up is needed",
			enableMultiNode: true,
			machines: []MachineStatus{
				{
					Machine:           &ghav1alpha1.RunnerMachine{Name: "worker1", Status: ghav1alpha1.RunnerMachineStatus{PowerState: ghav1alpha1.PowerStateOn}},
					Priority:          200,
					PreviouslyDesired: true,
					Ready:             true,
				},
				{
					Machine:           &ghav1alpha1.RunnerMachine{Name: "worker2", Status: ghav1alpha1.RunnerMachineStatus{PowerState: ghav1alpha1.PowerStateOn}},
					Priority:          100,
					PreviouslyDesired: true,
					Ready:             true,
				},
			},
			needsScaleUp:  true,
			wantSelected:  []string{"worker1", "worker2"},
			wantExhausted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := NewOrderedMachineSelector(tt.enableMultiNode)
			plan := selector.Select(tt.machines, tt.needsScaleUp)

			if plan.MultiNodeViolated != tt.wantViolated {
				t.Fatalf("expected MultiNodeViolated=%v, got %v", tt.wantViolated, plan.MultiNodeViolated)
			}

			if tt.wantViolated {
				return
			}

			if plan.NodesStarting != tt.wantStarting {
				t.Errorf("expected NodesStarting=%v, got %v", tt.wantStarting, plan.NodesStarting)
			}

			if plan.PoolExhausted != tt.wantExhausted {
				t.Errorf("expected PoolExhausted=%v, got %v", tt.wantExhausted, plan.PoolExhausted)
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
