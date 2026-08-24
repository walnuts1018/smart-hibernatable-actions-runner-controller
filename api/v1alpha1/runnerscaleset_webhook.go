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
	"fmt"
	"net/url"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// runnerscalesetlog is for logging in this package.
var runnerscalesetlog = logf.Log.WithName("runnerscaleset-resource")

// SetupWebhookWithManager registers the webhook with the manager.
func (r *RunnerScaleSet) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithDefaulter(r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-gha-walnuts-dev-v1alpha1-runnerscaleset,mutating=true,failurePolicy=fail,sideEffects=None,groups=gha.walnuts.dev,resources=runnerscalesets,verbs=create;update,versions=v1alpha1,name=mrunnerscaleset.kb.io,admissionReviewVersions=v1

var _ admission.Defaulter[*RunnerScaleSet] = &RunnerScaleSet{}

const (
	defaultRunnerGroup   = "default"
	defaultContainerName = "runner"
)

// Default implements admission.Defaulter so a webhook will be registered for the type
func (r *RunnerScaleSet) Default(_ context.Context, obj *RunnerScaleSet) error {
	runnerscalesetlog.Info("defaulting RunnerScaleSet", "name", obj.Name)

	if obj.Spec.GitHub.RunnerGroup == "" {
		obj.Spec.GitHub.RunnerGroup = defaultRunnerGroup
	}
	if obj.Spec.Scaling.MinRunners < 0 {
		obj.Spec.Scaling.MinRunners = 0
	}
	if obj.Spec.ContainerMode == "" {
		obj.Spec.ContainerMode = ContainerModeDind
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-gha-walnuts-dev-v1alpha1-runnerscaleset,mutating=false,failurePolicy=fail,sideEffects=None,groups=gha.walnuts.dev,resources=runnerscalesets,verbs=create;update,versions=v1alpha1,name=vrunnerscaleset.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*RunnerScaleSet] = &RunnerScaleSet{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type
func (r *RunnerScaleSet) ValidateCreate(_ context.Context, obj *RunnerScaleSet) (admission.Warnings, error) {
	runnerscalesetlog.Info("validate create RunnerScaleSet", "name", obj.Name)
	return nil, obj.validateRunnerScaleSet()
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type
func (r *RunnerScaleSet) ValidateUpdate(_ context.Context, oldObj, newObj *RunnerScaleSet) (admission.Warnings, error) {
	runnerscalesetlog.Info("validate update RunnerScaleSet", "name", newObj.Name)

	// 削除中のオブジェクト更新（Finalizer 削除など）は検証をスキップして Finalizer デッドロックを防止
	if newObj.DeletionTimestamp != nil {
		return nil, nil
	}

	var allErrs field.ErrorList

	// 不変フィールドの検証
	if newObj.Spec.GitHub.ConfigURL != oldObj.Spec.GitHub.ConfigURL {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "github", "configURL"),
			"field is immutable once created",
		))
	}

	if newObj.Spec.GitHub.ScaleSetName != oldObj.Spec.GitHub.ScaleSetName {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "github", "scaleSetName"),
			"field is immutable once created",
		))
	}

	if newObj.Spec.NodePoolRef.Name != oldObj.Spec.NodePoolRef.Name {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "nodePoolRef", "name"),
			"field is immutable once created",
		))
	}

	if err := appendStatusErrorCauses(&allErrs, newObj.validateRunnerScaleSet()); err != nil {
		return nil, err
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "RunnerScaleSet"},
			newObj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type
func (r *RunnerScaleSet) ValidateDelete(_ context.Context, obj *RunnerScaleSet) (admission.Warnings, error) {
	runnerscalesetlog.Info("validate delete RunnerScaleSet", "name", obj.Name)
	return nil, nil
}

func (r *RunnerScaleSet) validateRunnerScaleSet() error {
	var allErrs field.ErrorList

	r.validateGitHubSpec(&allErrs)
	r.validateScalingSpec(&allErrs)
	r.validateRunnerTemplateSpec(&allErrs)
	r.validateSecurityPolicy(&allErrs)
	r.validateReservedLabels(&allErrs)

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: GroupVersion.Group, Kind: "RunnerScaleSet"},
		r.Name,
		allErrs,
	)
}

func (r *RunnerScaleSet) validateGitHubSpec(allErrs *field.ErrorList) {
	if r.Spec.GitHub.ConfigURL == "" {
		*allErrs = append(*allErrs, field.Required(
			field.NewPath("spec", "github", "configURL"),
			"configURL must not be empty",
		))
	} else {
		parsedURL, err := url.ParseRequestURI(r.Spec.GitHub.ConfigURL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			*allErrs = append(*allErrs, field.Invalid(
				field.NewPath("spec", "github", "configURL"),
				r.Spec.GitHub.ConfigURL,
				"configURL must be a valid HTTP or HTTPS URL",
			))
		}
	}

	if r.Spec.GitHub.ScaleSetName == "" {
		*allErrs = append(*allErrs, field.Required(
			field.NewPath("spec", "github", "scaleSetName"),
			"scaleSetName must not be empty",
		))
	}

	if r.Spec.GitHub.CredentialsSecretRef.Name == "" {
		*allErrs = append(*allErrs, field.Required(
			field.NewPath("spec", "github", "credentialsSecretRef", "name"),
			"credentialsSecretRef name must not be empty",
		))
	}

	if r.Spec.NodePoolRef.Name == "" {
		*allErrs = append(*allErrs, field.Required(
			field.NewPath("spec", "nodePoolRef", "name"),
			"nodePoolRef name must not be empty",
		))
	}
}

func (r *RunnerScaleSet) validateScalingSpec(allErrs *field.ErrorList) {
	if r.Spec.Scaling.MinRunners < 0 {
		*allErrs = append(*allErrs, field.Invalid(
			field.NewPath("spec", "scaling", "minRunners"),
			r.Spec.Scaling.MinRunners,
			"minRunners must be greater than or equal to 0",
		))
	}

	if r.Spec.Scaling.MaxRunners != nil {
		if *r.Spec.Scaling.MaxRunners < 1 {
			*allErrs = append(*allErrs, field.Invalid(
				field.NewPath("spec", "scaling", "maxRunners"),
				*r.Spec.Scaling.MaxRunners,
				"maxRunners must be greater than or equal to 1",
			))
		}

		if r.Spec.Scaling.MinRunners > *r.Spec.Scaling.MaxRunners {
			*allErrs = append(*allErrs, field.Invalid(
				field.NewPath("spec", "scaling", "minRunners"),
				r.Spec.Scaling.MinRunners,
				"minRunners must be less than or equal to maxRunners",
			))
		}
	}
}

func (r *RunnerScaleSet) validateRunnerTemplateSpec(allErrs *field.ErrorList) {
	containers := r.Spec.Runner.Template.Spec.Containers
	if len(containers) == 0 {
		*allErrs = append(*allErrs, field.Required(
			field.NewPath("spec", "runner", "template", "spec", "containers"),
			"at least one container must be defined in runner pod template",
		))
		return
	}

	for ci, c := range containers {
		for ei, ev := range c.Env {
			if ev.Name == "ACTIONS_RUNNER_INPUT_JITCONFIG" {
				*allErrs = append(*allErrs, field.Forbidden(
					field.NewPath("spec", "runner", "template", "spec", "containers").Index(ci).Child("env").Index(ei),
					"ACTIONS_RUNNER_INPUT_JITCONFIG is a reserved environment variable managed by SHARC",
				))
			}
		}
	}
}

func (r *RunnerScaleSet) validateSecurityPolicy(allErrs *field.ErrorList) {
	podSpec := r.Spec.Runner.Template.Spec
	if podSpec.HostNetwork {
		*allErrs = append(*allErrs, field.Forbidden(
			field.NewPath("spec", "runner", "template", "spec", "hostNetwork"),
			"hostNetwork is forbidden for ephemeral runner pods",
		))
	}
	if podSpec.HostPID {
		*allErrs = append(*allErrs, field.Forbidden(
			field.NewPath("spec", "runner", "template", "spec", "hostPID"),
			"hostPID is forbidden for ephemeral runner pods",
		))
	}
	if podSpec.HostIPC {
		*allErrs = append(*allErrs, field.Forbidden(
			field.NewPath("spec", "runner", "template", "spec", "hostIPC"),
			"hostIPC is forbidden for ephemeral runner pods",
		))
	}
	for vi, v := range podSpec.Volumes {
		if v.HostPath != nil {
			*allErrs = append(*allErrs, field.Forbidden(
				field.NewPath("spec", "runner", "template", "spec", "volumes").Index(vi).Child("hostPath"),
				"hostPath volumes are forbidden for ephemeral runner pods; use emptyDir or dedicated storage",
			))
		}
	}
}

func (r *RunnerScaleSet) validateReservedLabels(allErrs *field.ErrorList) {
	for k := range r.Spec.Runner.Template.Labels {
		if k == "gha.walnuts.dev/managed-by" || k == "gha.walnuts.dev/scaleset-uid" || k == "gha.walnuts.dev/scaleset-name" || k == "gha.walnuts.dev/runner-uid" || k == "gha.walnuts.dev/runner-name" {
			*allErrs = append(*allErrs, field.Forbidden(
				field.NewPath("spec", "runner", "template", "metadata", "labels").Key(k),
				fmt.Sprintf("label %q is reserved and managed by SHARC", k),
			))
		}
	}
}
