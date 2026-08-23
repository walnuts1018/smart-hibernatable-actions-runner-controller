/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func TestRunnerMachineDefaulting(t *testing.T) {
	m := &RunnerMachine{
		Name:      "test-machine",
		Namespace: "default",
		Spec: RunnerMachineSpec{
			ClusterRef:         corev1.LocalObjectReference{Name: "test-cluster"},
			KubernetesNodeName: "node-1",
			Capacity:           RunnerMachineCapacity{Runners: 4},
			Redfish: RedfishSpec{
				Endpoint:             "https://192.168.1.100",
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "redfish-secret"},
			},
		},
	}

	if err := m.Default(context.Background(), m); err != nil {
		t.Fatalf("Default() error = %v", err)
	}

	if m.Spec.Priority != 100 {
		t.Errorf("expected default priority 100, got %d", m.Spec.Priority)
	}
	if m.Spec.Redfish.SystemID != "1" {
		t.Errorf("expected default systemID '1', got %q", m.Spec.Redfish.SystemID)
	}
	if m.Spec.Redfish.Power.ShutdownTimeout == nil || m.Spec.Redfish.Power.ShutdownTimeout.Duration != 3*time.Minute {
		t.Errorf("expected default shutdownTimeout 3m, got %v", m.Spec.Redfish.Power.ShutdownTimeout)
	}
	if m.Spec.Redfish.Power.ForceOffAfter == nil || m.Spec.Redfish.Power.ForceOffAfter.Duration != 0 {
		t.Errorf("expected default forceOffAfter 0, got %v", m.Spec.Redfish.Power.ForceOffAfter)
	}
}

func TestRunnerMachineValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m *RunnerMachine)
		wantErr bool
	}{
		{
			name:    "valid RunnerMachine",
			mutate:  func(m *RunnerMachine) {},
			wantErr: false,
		},
		{
			name: "empty clusterRef",
			mutate: func(m *RunnerMachine) {
				m.Spec.ClusterRef.Name = ""
			},
			wantErr: true,
		},
		{
			name: "empty kubernetesNodeName",
			mutate: func(m *RunnerMachine) {
				m.Spec.KubernetesNodeName = ""
			},
			wantErr: true,
		},
		{
			name: "capacity runners < 1",
			mutate: func(m *RunnerMachine) {
				m.Spec.Capacity.Runners = 0
			},
			wantErr: true,
		},
		{
			name: "invalid redfish endpoint",
			mutate: func(m *RunnerMachine) {
				m.Spec.Redfish.Endpoint = "invalid-url"
			},
			wantErr: true,
		},
		{
			name: "empty redfish credentialsSecretRef",
			mutate: func(m *RunnerMachine) {
				m.Spec.Redfish.CredentialsSecretRef.Name = ""
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &RunnerMachine{
				Name:      "test-machine",
				Namespace: "default",
				Spec: RunnerMachineSpec{
					ClusterRef:         corev1.LocalObjectReference{Name: "test-cluster"},
					KubernetesNodeName: "node-1",
					Capacity:           RunnerMachineCapacity{Runners: 4},
					Redfish: RedfishSpec{
						Endpoint:             "https://192.168.1.100",
						CredentialsSecretRef: corev1.LocalObjectReference{Name: "redfish-secret"},
					},
				},
			}

			tt.mutate(m)
			_, err := m.ValidateCreate(context.Background(), m)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunnerMachineValidateUpdate(t *testing.T) {
	oldMachine := &RunnerMachine{
		Name:      "test-machine",
		Namespace: "default",
		Spec: RunnerMachineSpec{
			ClusterRef:         corev1.LocalObjectReference{Name: "test-cluster"},
			KubernetesNodeName: "node-1",
			Capacity:           RunnerMachineCapacity{Runners: 4},
			Redfish: RedfishSpec{
				Endpoint:             "https://192.168.1.100",
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "redfish-secret"},
			},
		},
	}

	t.Run("valid update", func(t *testing.T) {
		newMachine := oldMachine.DeepCopy()
		newMachine.Spec.Capacity.Runners = 8
		_, err := newMachine.ValidateUpdate(context.Background(), oldMachine, newMachine)
		if err != nil {
			t.Errorf("expected valid update, got error: %v", err)
		}
	})

	t.Run("immutable clusterRef modified", func(t *testing.T) {
		newMachine := oldMachine.DeepCopy()
		newMachine.Spec.ClusterRef.Name = "other-cluster"
		_, err := newMachine.ValidateUpdate(context.Background(), oldMachine, newMachine)
		if err == nil {
			t.Errorf("expected error modifying immutable clusterRef, got nil")
		}
	})

	t.Run("immutable kubernetesNodeName modified", func(t *testing.T) {
		newMachine := oldMachine.DeepCopy()
		newMachine.Spec.KubernetesNodeName = "other-node"
		_, err := newMachine.ValidateUpdate(context.Background(), oldMachine, newMachine)
		if err == nil {
			t.Errorf("expected error modifying immutable kubernetesNodeName, got nil")
		}
	})
}
