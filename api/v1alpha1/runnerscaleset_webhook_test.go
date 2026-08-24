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

func TestRunnerScaleSetDefaulting(t *testing.T) {
	ss := &RunnerScaleSet{
		Name:      "test-scaleset",
		Namespace: "default",
		Spec: RunnerScaleSetSpec{
			GitHub: GitHubScaleSetSpec{
				ConfigURL:            "https://github.com/example-org",
				ScaleSetName:         "test-set",
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "gha-secret"},
			},
			NodePoolRef: corev1.LocalObjectReference{Name: "test-pool"},
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

	if err := ss.Default(context.Background(), ss); err != nil {
		t.Fatalf("Default() error = %v", err)
	}

	if ss.Spec.GitHub.RunnerGroup != "default" {
		t.Errorf("expected RunnerGroup 'default', got %q", ss.Spec.GitHub.RunnerGroup)
	}
	if ss.Spec.Scaling.MinRunners != 0 {
		t.Errorf("expected MinRunners 0, got %d", ss.Spec.Scaling.MinRunners)
	}
	if ss.Spec.ContainerMode != ContainerModeDind {
		t.Errorf("expected ContainerMode %q, got %q", ContainerModeDind, ss.Spec.ContainerMode)
	}
}

func TestRunnerScaleSetValidateCreate(t *testing.T) {
	two := int32(2)
	tests := []struct {
		name    string
		mutate  func(ss *RunnerScaleSet)
		wantErr bool
	}{
		{
			name:    "valid RunnerScaleSet",
			mutate:  func(ss *RunnerScaleSet) {},
			wantErr: false,
		},
		{
			name: "empty configURL",
			mutate: func(ss *RunnerScaleSet) {
				ss.Spec.GitHub.ConfigURL = ""
			},
			wantErr: true,
		},
		{
			name: "invalid configURL format",
			mutate: func(ss *RunnerScaleSet) {
				ss.Spec.GitHub.ConfigURL = "not-a-url"
			},
			wantErr: true,
		},
		{
			name: "empty scaleSetName",
			mutate: func(ss *RunnerScaleSet) {
				ss.Spec.GitHub.ScaleSetName = ""
			},
			wantErr: true,
		},
		{
			name: "empty credentialsSecretRef",
			mutate: func(ss *RunnerScaleSet) {
				ss.Spec.GitHub.CredentialsSecretRef.Name = ""
			},
			wantErr: true,
		},
		{
			name: "empty nodePoolRef",
			mutate: func(ss *RunnerScaleSet) {
				ss.Spec.NodePoolRef.Name = ""
			},
			wantErr: true,
		},
		{
			name: "minRunners > maxRunners",
			mutate: func(ss *RunnerScaleSet) {
				ss.Spec.Scaling.MinRunners = 5
				maxVal := int32(2)
				ss.Spec.Scaling.MaxRunners = &maxVal
			},
			wantErr: true,
		},
		{
			name: "maxRunners < 1",
			mutate: func(ss *RunnerScaleSet) {
				zero := int32(0)
				ss.Spec.Scaling.MaxRunners = &zero
			},
			wantErr: true,
		},
		{
			name: "no containers in runner template",
			mutate: func(ss *RunnerScaleSet) {
				ss.Spec.Runner.Template.Spec.Containers = nil
			},
			wantErr: true,
		},
		{
			name: "reserved env variable ACTIONS_RUNNER_INPUT_JITCONFIG",
			mutate: func(ss *RunnerScaleSet) {
				ss.Spec.Runner.Template.Spec.Containers[0].Env = append(
					ss.Spec.Runner.Template.Spec.Containers[0].Env,
					corev1.EnvVar{Name: "ACTIONS_RUNNER_INPUT_JITCONFIG", Value: "foo"},
				)
			},
			wantErr: true,
		},
		{
			name: "reserved label sharc.walnuts.dev/managed-by",
			mutate: func(ss *RunnerScaleSet) {
				if ss.Spec.Runner.Template.Labels == nil {
					ss.Spec.Runner.Template.Labels = make(map[string]string)
				}
				ss.Spec.Runner.Template.Labels["sharc.walnuts.dev/managed-by"] = "custom"
			},
			wantErr: true,
		},
		{
			name: "privileged container allowed",
			mutate: func(ss *RunnerScaleSet) {
				priv := true
				ss.Spec.Runner.Template.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
					Privileged: &priv,
				}
			},
			wantErr: false,
		},
		{
			name: "hostNetwork forbidden",
			mutate: func(ss *RunnerScaleSet) {
				ss.Spec.Runner.Template.Spec.HostNetwork = true
			},
			wantErr: true,
		},
		{
			name: "hostPath forbidden",
			mutate: func(ss *RunnerScaleSet) {
				vol := corev1.Volume{Name: "host-vol",
					HostPath: &corev1.HostPathVolumeSource{Path: "/var/run/docker.sock"}}
				ss.Spec.Runner.Template.Spec.Volumes = []corev1.Volume{vol}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := &RunnerScaleSet{
				Name:      "test-scaleset",
				Namespace: "default",
				Spec: RunnerScaleSetSpec{
					GitHub: GitHubScaleSetSpec{
						ConfigURL:            "https://github.com/example-org",
						ScaleSetName:         "test-set",
						RunnerGroup:          "default",
						CredentialsSecretRef: corev1.LocalObjectReference{Name: "gha-secret"},
					},
					NodePoolRef: corev1.LocalObjectReference{Name: "test-pool"},
					Scaling: RunnerScaleSetScalingSpec{
						MinRunners: 0,
						MaxRunners: &two,
					},
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

			tt.mutate(ss)
			_, err := ss.ValidateCreate(context.Background(), ss)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunnerScaleSetValidateUpdate(t *testing.T) {
	two := int32(2)
	oldSS := &RunnerScaleSet{
		Name:      "test-scaleset",
		Namespace: "default",
		Spec: RunnerScaleSetSpec{
			GitHub: GitHubScaleSetSpec{
				ConfigURL:            "https://github.com/example-org",
				ScaleSetName:         "test-set",
				RunnerGroup:          "default",
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "gha-secret"},
			},
			NodePoolRef: corev1.LocalObjectReference{Name: "test-pool"},
			Scaling: RunnerScaleSetScalingSpec{
				MinRunners: 0,
				MaxRunners: &two,
			},
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

	t.Run("valid update (scaling only)", func(t *testing.T) {
		newSS := oldSS.DeepCopy()
		five := int32(5)
		newSS.Spec.Scaling.MaxRunners = &five
		_, err := newSS.ValidateUpdate(context.Background(), oldSS, newSS)
		if err != nil {
			t.Errorf("expected valid update, got error: %v", err)
		}
	})

	t.Run("immutable configURL modified", func(t *testing.T) {
		newSS := oldSS.DeepCopy()
		newSS.Spec.GitHub.ConfigURL = "https://github.com/other-org"
		_, err := newSS.ValidateUpdate(context.Background(), oldSS, newSS)
		if err == nil {
			t.Errorf("expected error modifying immutable configURL, got nil")
		}
	})

	t.Run("immutable scaleSetName modified", func(t *testing.T) {
		newSS := oldSS.DeepCopy()
		newSS.Spec.GitHub.ScaleSetName = "new-name"
		_, err := newSS.ValidateUpdate(context.Background(), oldSS, newSS)
		if err == nil {
			t.Errorf("expected error modifying immutable scaleSetName, got nil")
		}
	})

	t.Run("immutable nodePoolRef modified", func(t *testing.T) {
		newSS := oldSS.DeepCopy()
		newSS.Spec.NodePoolRef.Name = "new-pool"
		_, err := newSS.ValidateUpdate(context.Background(), oldSS, newSS)
		if err == nil {
			t.Errorf("expected error modifying immutable nodePoolRef, got nil")
		}
	})
}
