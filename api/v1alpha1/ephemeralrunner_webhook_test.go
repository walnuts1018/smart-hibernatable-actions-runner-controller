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

	corev1 "k8s.io/api/core/v1"
)

func TestEphemeralRunnerValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(r *EphemeralRunner)
		wantErr bool
	}{
		{
			name:    "valid EphemeralRunner",
			mutate:  func(r *EphemeralRunner) {},
			wantErr: false,
		},
		{
			name: "empty scaleSetRef",
			mutate: func(r *EphemeralRunner) {
				r.Spec.ScaleSetRef.Name = ""
			},
			wantErr: true,
		},
		{
			name: "empty runnerName",
			mutate: func(r *EphemeralRunner) {
				r.Spec.RunnerName = ""
			},
			wantErr: true,
		},
		{
			name: "invalid runnerName (uppercase)",
			mutate: func(r *EphemeralRunner) {
				r.Spec.RunnerName = "INVALID_NAME"
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &EphemeralRunner{
				Name:      "test-runner-abc",
				Namespace: "default",
				Spec: EphemeralRunnerSpec{
					ScaleSetRef: corev1.LocalObjectReference{Name: "test-scaleset"},
					RunnerName:  "test-runner-abc",
				},
			}

			tt.mutate(runner)
			_, err := runner.ValidateCreate(context.Background(), runner)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEphemeralRunnerValidateUpdate(t *testing.T) {
	oldRunner := &EphemeralRunner{
		Name:      "test-runner-abc",
		Namespace: "default",
		Spec: EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "test-scaleset"},
			RunnerName:  "test-runner-abc",
		},
	}

	t.Run("valid update (same spec)", func(t *testing.T) {
		newRunner := oldRunner.DeepCopy()
		_, err := newRunner.ValidateUpdate(context.Background(), oldRunner, newRunner)
		if err != nil {
			t.Errorf("expected valid update, got error: %v", err)
		}
	})

	t.Run("immutable scaleSetRef modified", func(t *testing.T) {
		newRunner := oldRunner.DeepCopy()
		newRunner.Spec.ScaleSetRef.Name = "other-scaleset"
		_, err := newRunner.ValidateUpdate(context.Background(), oldRunner, newRunner)
		if err == nil {
			t.Errorf("expected error modifying immutable scaleSetRef, got nil")
		}
	})

	t.Run("immutable runnerName modified", func(t *testing.T) {
		newRunner := oldRunner.DeepCopy()
		newRunner.Spec.RunnerName = "other-runner-name"
		_, err := newRunner.ValidateUpdate(context.Background(), oldRunner, newRunner)
		if err == nil {
			t.Errorf("expected error modifying immutable runnerName, got nil")
		}
	})
}
