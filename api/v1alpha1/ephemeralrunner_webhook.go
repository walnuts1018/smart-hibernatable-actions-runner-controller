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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	validationutil "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// ephemeralrunnerlog is for logging in this package.
var ephemeralrunnerlog = logf.Log.WithName("ephemeralrunner-resource")

// SetupWebhookWithManager registers the webhook with the manager.
func (r *EphemeralRunner) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/validate-sharc-walnuts-dev-v1alpha1-ephemeralrunner,mutating=false,failurePolicy=fail,sideEffects=None,groups=sharc.walnuts.dev,resources=ephemeralrunners,verbs=create;update,versions=v1alpha1,name=vephemeralrunner.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*EphemeralRunner] = &EphemeralRunner{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type
func (r *EphemeralRunner) ValidateCreate(_ context.Context, obj *EphemeralRunner) (admission.Warnings, error) {
	ephemeralrunnerlog.Info("validate create EphemeralRunner", "name", obj.Name)
	return nil, obj.validateEphemeralRunner()
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type
func (r *EphemeralRunner) ValidateUpdate(_ context.Context, oldObj, newObj *EphemeralRunner) (admission.Warnings, error) {
	ephemeralrunnerlog.Info("validate update EphemeralRunner", "name", newObj.Name)

	var allErrs field.ErrorList
	validateImmutableString(&allErrs, field.NewPath("spec", "scaleSetRef", "name"), oldObj.Spec.ScaleSetRef.Name, newObj.Spec.ScaleSetRef.Name)
	validateImmutableString(&allErrs, field.NewPath("spec", "runnerName"), oldObj.Spec.RunnerName, newObj.Spec.RunnerName)

	if err := appendStatusErrorCauses(&allErrs, newObj.validateEphemeralRunner()); err != nil {
		return nil, err
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "EphemeralRunner"},
			newObj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type
func (r *EphemeralRunner) ValidateDelete(_ context.Context, obj *EphemeralRunner) (admission.Warnings, error) {
	ephemeralrunnerlog.Info("validate delete EphemeralRunner", "name", obj.Name)
	return nil, nil
}

func (r *EphemeralRunner) validateEphemeralRunner() error {
	var allErrs field.ErrorList

	// ScaleSetRef検証
	if r.Spec.ScaleSetRef.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "scaleSetRef", "name"),
			"scaleSetRef name must not be empty",
		))
	}

	// RunnerName検証
	if r.Spec.RunnerName == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "runnerName"),
			"runnerName must not be empty",
		))
	} else {
		if errs := validationutil.IsDNS1123Subdomain(r.Spec.RunnerName); len(errs) > 0 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "runnerName"),
				r.Spec.RunnerName,
				fmt.Sprintf("runnerName must be a valid DNS-1123 subdomain: %v", errs),
			))
		}
	}

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: GroupVersion.Group, Kind: "EphemeralRunner"},
		r.Name,
		allErrs,
	)
}
