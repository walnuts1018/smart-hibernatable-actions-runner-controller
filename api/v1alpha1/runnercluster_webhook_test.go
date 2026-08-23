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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRunnerClusterDefaulting(t *testing.T) {
	cluster := &RunnerCluster{
		Name:      "test-cluster",
		Namespace: "default",
		Spec: RunnerClusterSpec{
			KubeconfigSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "cluster-kubeconfig"},
				Key:                  "value",
			},
		},
	}

	if err := cluster.Default(context.Background(), cluster); err != nil {
		t.Fatalf("Default() error = %v", err)
	}

	if cluster.Spec.RunnerNamespace != "gha-runners" {
		t.Errorf("expected default runnerNamespace 'gha-runners', got %q", cluster.Spec.RunnerNamespace)
	}
	if cluster.Spec.Readiness.APIRequestTimeout == nil || cluster.Spec.Readiness.APIRequestTimeout.Duration != 5*time.Second {
		t.Errorf("expected default apiRequestTimeout 5s, got %v", cluster.Spec.Readiness.APIRequestTimeout)
	}
	if cluster.Spec.Readiness.NodeReadyTimeout == nil || cluster.Spec.Readiness.NodeReadyTimeout.Duration != 10*time.Minute {
		t.Errorf("expected default nodeReadyTimeout 10m, got %v", cluster.Spec.Readiness.NodeReadyTimeout)
	}
}

func TestRunnerClusterValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *RunnerCluster)
		wantErr bool
	}{
		{
			name:    "valid RunnerCluster",
			mutate:  func(c *RunnerCluster) {},
			wantErr: false,
		},
		{
			name: "empty secret name",
			mutate: func(c *RunnerCluster) {
				c.Spec.KubeconfigSecretRef.Name = ""
			},
			wantErr: true,
		},
		{
			name: "empty secret key",
			mutate: func(c *RunnerCluster) {
				c.Spec.KubeconfigSecretRef.Key = ""
			},
			wantErr: true,
		},
		{
			name: "invalid namespace format",
			mutate: func(c *RunnerCluster) {
				c.Spec.RunnerNamespace = "INVALID_NAME_!!"
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &RunnerCluster{
				Name:      "test-cluster",
				Namespace: "default",
				Spec: RunnerClusterSpec{
					KubeconfigSecretRef: corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "cluster-kubeconfig"},
						Key:                  "value",
					},
					RunnerNamespace: "gha-runners",
				},
			}

			tt.mutate(cluster)
			_, err := cluster.ValidateCreate(context.Background(), cluster)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunnerClusterValidateUpdate(t *testing.T) {
	oldCluster := &RunnerCluster{
		Name:      "test-cluster",
		Namespace: "default",
		Spec: RunnerClusterSpec{
			KubeconfigSecretRef: corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "cluster-kubeconfig"},
				Key:                  "value",
			},
			RunnerNamespace: "gha-runners",
		},
	}

	t.Run("valid update", func(t *testing.T) {
		newCluster := oldCluster.DeepCopy()
		newCluster.Spec.Readiness.APIRequestTimeout = &metav1.Duration{Duration: 10 * time.Second}
		_, err := newCluster.ValidateUpdate(context.Background(), oldCluster, newCluster)
		if err != nil {
			t.Errorf("expected valid update, got error: %v", err)
		}
	})

	t.Run("immutable runnerNamespace modified", func(t *testing.T) {
		newCluster := oldCluster.DeepCopy()
		newCluster.Spec.RunnerNamespace = "other-namespace"
		_, err := newCluster.ValidateUpdate(context.Background(), oldCluster, newCluster)
		if err == nil {
			t.Errorf("expected error modifying immutable runnerNamespace, got nil")
		}
	})
}
