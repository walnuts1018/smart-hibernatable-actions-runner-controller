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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// ephemeralrunnersetlog is for logging in this package.
var ephemeralrunnersetlog = logf.Log.WithName("ephemeralrunnerset-resource")

// SetupWebhookWithManager registers the webhook with the manager.
func (r *EphemeralRunnerSet) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithDefaulter(r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-gha-walnuts-dev-v1alpha1-ephemeralrunnerset,mutating=true,failurePolicy=fail,sideEffects=None,groups=gha.walnuts.dev,resources=ephemeralrunnersets,verbs=create;update,versions=v1alpha1,name=mephemeralrunnerset.kb.io,admissionReviewVersions=v1

var _ admission.Defaulter[*EphemeralRunnerSet] = &EphemeralRunnerSet{}

// Default implements admission.Defaulter so a webhook will be registered for the type
func (r *EphemeralRunnerSet) Default(_ context.Context, obj *EphemeralRunnerSet) error {
	ephemeralrunnersetlog.Info("defaulting EphemeralRunnerSet", "name", obj.Name)
	if obj.Spec.Replicas == nil {
		zero := int32(0)
		obj.Spec.Replicas = &zero
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-gha-walnuts-dev-v1alpha1-ephemeralrunnerset,mutating=false,failurePolicy=fail,sideEffects=None,groups=gha.walnuts.dev,resources=ephemeralrunnersets,verbs=create;update,versions=v1alpha1,name=vephemeralrunnerset.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*EphemeralRunnerSet] = &EphemeralRunnerSet{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type
func (r *EphemeralRunnerSet) ValidateCreate(_ context.Context, obj *EphemeralRunnerSet) (admission.Warnings, error) {
	ephemeralrunnersetlog.Info("validate create EphemeralRunnerSet", "name", obj.Name)
	return nil, obj.validateEphemeralRunnerSet()
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type
func (r *EphemeralRunnerSet) ValidateUpdate(_ context.Context, oldObj, newObj *EphemeralRunnerSet) (admission.Warnings, error) {
	ephemeralrunnersetlog.Info("validate update EphemeralRunnerSet", "name", newObj.Name)

	if newObj.DeletionTimestamp != nil {
		return nil, nil
	}

	var allErrs field.ErrorList
	validateImmutableString(&allErrs, field.NewPath("spec", "scaleSetRef", "name"), oldObj.Spec.ScaleSetRef.Name, newObj.Spec.ScaleSetRef.Name)

	if err := appendStatusErrorCauses(&allErrs, newObj.validateEphemeralRunnerSet()); err != nil {
		return nil, err
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "EphemeralRunnerSet"},
			newObj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type
func (r *EphemeralRunnerSet) ValidateDelete(_ context.Context, obj *EphemeralRunnerSet) (admission.Warnings, error) {
	ephemeralrunnersetlog.Info("validate delete EphemeralRunnerSet", "name", obj.Name)
	return nil, nil
}

func (r *EphemeralRunnerSet) validateEphemeralRunnerSet() error {
	var allErrs field.ErrorList

	if r.Spec.ScaleSetRef.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "scaleSetRef", "name"),
			"scaleSetRef name must not be empty",
		))
	}

	if r.Spec.Replicas != nil && *r.Spec.Replicas < 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "replicas"),
			*r.Spec.Replicas,
			"replicas must be greater than or equal to 0",
		))
	}

	containers := r.Spec.Runner.Template.Spec.Containers
	if len(containers) == 0 {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "runner", "template", "spec", "containers"),
			"at least one container must be defined in runner pod template",
		))
	}

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: GroupVersion.Group, Kind: "EphemeralRunnerSet"},
		r.Name,
		allErrs,
	)
}
