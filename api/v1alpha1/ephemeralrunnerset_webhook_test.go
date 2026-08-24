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

func TestEphemeralRunnerSetDefaulting(t *testing.T) {
	ers := &EphemeralRunnerSet{
		Name:      "test-ers",
		Namespace: "default",
		Spec: EphemeralRunnerSetSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "test-scaleset"},
			Runner: RunnerTemplateSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "runner", Image: "runner:latest"},
						},
					},
				},
			},
		},
	}

	if err := ers.Default(context.Background(), ers); err != nil {
		t.Fatalf("Default() error = %v", err)
	}

	if ers.Spec.Replicas == nil || *ers.Spec.Replicas != 0 {
		t.Errorf("expected default replicas 0, got %v", ers.Spec.Replicas)
	}
}

func TestEphemeralRunnerSetValidateCreate(t *testing.T) {
	zero := int32(0)
	tests := []struct {
		name    string
		mutate  func(ers *EphemeralRunnerSet)
		wantErr bool
	}{
		{
			name:    "valid EphemeralRunnerSet",
			mutate:  func(ers *EphemeralRunnerSet) {},
			wantErr: false,
		},
		{
			name: "empty scaleSetRef",
			mutate: func(ers *EphemeralRunnerSet) {
				ers.Spec.ScaleSetRef.Name = ""
			},
			wantErr: true,
		},
		{
			name: "negative replicas",
			mutate: func(ers *EphemeralRunnerSet) {
				neg := int32(-1)
				ers.Spec.Replicas = &neg
			},
			wantErr: true,
		},
		{
			name: "no containers",
			mutate: func(ers *EphemeralRunnerSet) {
				ers.Spec.Runner.Template.Spec.Containers = nil
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ers := &EphemeralRunnerSet{
				Name:      "test-ers",
				Namespace: "default",
				Spec: EphemeralRunnerSetSpec{
					ScaleSetRef: corev1.LocalObjectReference{Name: "test-scaleset"},
					Replicas:    &zero,
					Runner: RunnerTemplateSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{Name: "runner", Image: "runner:latest"},
								},
							},
						},
					},
				},
			}

			tt.mutate(ers)
			_, err := ers.ValidateCreate(context.Background(), ers)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEphemeralRunnerSetValidateUpdate(t *testing.T) {
	zero := int32(0)
	oldERS := &EphemeralRunnerSet{
		Name:      "test-ers",
		Namespace: "default",
		Spec: EphemeralRunnerSetSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "test-scaleset"},
			Replicas:    &zero,
			Runner: RunnerTemplateSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "runner", Image: "runner:latest"},
						},
					},
				},
			},
		},
	}

	t.Run("valid update (replicas)", func(t *testing.T) {
		newERS := oldERS.DeepCopy()
		three := int32(3)
		newERS.Spec.Replicas = &three
		_, err := newERS.ValidateUpdate(context.Background(), oldERS, newERS)
		if err != nil {
			t.Errorf("expected valid update, got error: %v", err)
		}
	})

	t.Run("immutable scaleSetRef modified", func(t *testing.T) {
		newERS := oldERS.DeepCopy()
		newERS.Spec.ScaleSetRef.Name = "new-scaleset"
		_, err := newERS.ValidateUpdate(context.Background(), oldERS, newERS)
		if err == nil {
			t.Errorf("expected error modifying immutable scaleSetRef, got nil")
		}
	})
}
