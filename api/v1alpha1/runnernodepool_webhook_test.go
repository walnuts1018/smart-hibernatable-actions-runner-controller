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

func TestRunnerNodePoolDefaulting(t *testing.T) {
	pool := &RunnerNodePool{
		Name:      "test-pool",
		Namespace: "default",
		Spec: RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "test-cluster"},
		},
	}

	if err := pool.Default(context.Background(), pool); err != nil {
		t.Fatalf("Default() error = %v", err)
	}

	if pool.Spec.Scaling.MinNodes != 0 {
		t.Errorf("expected default minNodes 0, got %d", pool.Spec.Scaling.MinNodes)
	}
	if pool.Spec.Scaling.ScaleDownDelay == nil || pool.Spec.Scaling.ScaleDownDelay.Duration != 10*time.Minute {
		t.Errorf("expected default scaleDownDelay 10m, got %v", pool.Spec.Scaling.ScaleDownDelay)
	}
}

func TestRunnerNodePoolValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(p *RunnerNodePool)
		wantErr bool
	}{
		{
			name:    "valid RunnerNodePool",
			mutate:  func(p *RunnerNodePool) {},
			wantErr: false,
		},
		{
			name: "empty clusterRef",
			mutate: func(p *RunnerNodePool) {
				p.Spec.ClusterRef.Name = ""
			},
			wantErr: true,
		},
		{
			name: "minNodes > maxNodes",
			mutate: func(p *RunnerNodePool) {
				p.Spec.Scaling.MinNodes = 5
				two := int32(2)
				p.Spec.Scaling.MaxNodes = &two
			},
			wantErr: true,
		},
		{
			name: "maxNodes < 1",
			mutate: func(p *RunnerNodePool) {
				zero := int32(0)
				p.Spec.Scaling.MaxNodes = &zero
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			one := int32(1)
			pool := &RunnerNodePool{
				Name:      "test-pool",
				Namespace: "default",
				Spec: RunnerNodePoolSpec{
					ClusterRef: corev1.LocalObjectReference{Name: "test-cluster"},
					Scaling: RunnerNodePoolScalingSpec{
						MinNodes: 0,
						MaxNodes: &one,
					},
				},
			}

			tt.mutate(pool)
			_, err := pool.ValidateCreate(context.Background(), pool)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunnerNodePoolValidateUpdate(t *testing.T) {
	one := int32(1)
	oldPool := &RunnerNodePool{
		Name:      "test-pool",
		Namespace: "default",
		Spec: RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "test-cluster"},
			Scaling: RunnerNodePoolScalingSpec{
				MinNodes: 0,
				MaxNodes: &one,
			},
		},
	}

	t.Run("valid update", func(t *testing.T) {
		newPool := oldPool.DeepCopy()
		three := int32(3)
		newPool.Spec.Scaling.MaxNodes = &three
		_, err := newPool.ValidateUpdate(context.Background(), oldPool, newPool)
		if err != nil {
			t.Errorf("expected valid update, got error: %v", err)
		}
	})

	t.Run("immutable clusterRef modified", func(t *testing.T) {
		newPool := oldPool.DeepCopy()
		newPool.Spec.ClusterRef.Name = "other-cluster"
		_, err := newPool.ValidateUpdate(context.Background(), oldPool, newPool)
		if err == nil {
			t.Errorf("expected error modifying immutable clusterRef, got nil")
		}
	})
}
