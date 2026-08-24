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
	"net/url"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// runnermachinelog is for logging in this package.
var runnermachinelog = logf.Log.WithName("runnermachine-resource")

// SetupWebhookWithManager registers the webhook with the manager.
func (r *RunnerMachine) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithDefaulter(r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-gha-walnuts-dev-v1alpha1-runnermachine,mutating=true,failurePolicy=fail,sideEffects=None,groups=gha.walnuts.dev,resources=runnermachines,verbs=create;update,versions=v1alpha1,name=mrunnermachine.kb.io,admissionReviewVersions=v1

var _ admission.Defaulter[*RunnerMachine] = &RunnerMachine{}

// Default implements admission.Defaulter so a webhook will be registered for the type
func (r *RunnerMachine) Default(_ context.Context, obj *RunnerMachine) error {
	runnermachinelog.Info("defaulting RunnerMachine", "name", obj.Name)

	if obj.Spec.PowerPolicy == "" {
		obj.Spec.PowerPolicy = RunnerMachinePowerPolicyOnDemand
	}
	if obj.Spec.Redfish.SystemID == "" {
		obj.Spec.Redfish.SystemID = "1"
	}
	if obj.Spec.Redfish.Power.Shutdown.Timeout == nil {
		obj.Spec.Redfish.Power.Shutdown.Timeout = &metav1.Duration{Duration: 3 * time.Minute}
	}
	if obj.Spec.Redfish.Power.Shutdown.TimeoutPolicy == "" {
		obj.Spec.Redfish.Power.Shutdown.TimeoutPolicy = RedfishTimeoutPolicyAbort
	}
	if obj.Spec.Drain == nil {
		obj.Spec.Drain = &MachineDrainSpec{
			Timeout: &metav1.Duration{Duration: 10 * time.Minute},
		}
	} else if obj.Spec.Drain.Timeout == nil {
		obj.Spec.Drain.Timeout = &metav1.Duration{Duration: 10 * time.Minute}
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-gha-walnuts-dev-v1alpha1-runnermachine,mutating=false,failurePolicy=fail,sideEffects=None,groups=gha.walnuts.dev,resources=runnermachines,verbs=create;update,versions=v1alpha1,name=vrunnermachine.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*RunnerMachine] = &RunnerMachine{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type
func (r *RunnerMachine) ValidateCreate(_ context.Context, obj *RunnerMachine) (admission.Warnings, error) {
	runnermachinelog.Info("validate create RunnerMachine", "name", obj.Name)
	return nil, obj.validateRunnerMachine()
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type
func (r *RunnerMachine) ValidateUpdate(_ context.Context, oldObj, newObj *RunnerMachine) (admission.Warnings, error) {
	runnermachinelog.Info("validate update RunnerMachine", "name", newObj.Name)

	// 削除中のオブジェクト更新（Finalizer 削除など）は検証をスキップして Finalizer デッドロックを防止
	if newObj.DeletionTimestamp != nil {
		return nil, nil
	}

	var allErrs field.ErrorList
	validateImmutableString(&allErrs, field.NewPath("spec", "clusterRef", "name"), oldObj.Spec.ClusterRef.Name, newObj.Spec.ClusterRef.Name)
	validateImmutableString(&allErrs, field.NewPath("spec", "nodeName"), oldObj.Spec.NodeName, newObj.Spec.NodeName)

	if err := appendStatusErrorCauses(&allErrs, newObj.validateRunnerMachine()); err != nil {
		return nil, err
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "RunnerMachine"},
			newObj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type
func (r *RunnerMachine) ValidateDelete(_ context.Context, obj *RunnerMachine) (admission.Warnings, error) {
	runnermachinelog.Info("validate delete RunnerMachine", "name", obj.Name)
	return nil, nil
}

func (r *RunnerMachine) validateRunnerMachine() error {
	var allErrs field.ErrorList

	// ClusterRef検証
	if r.Spec.ClusterRef.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "clusterRef", "name"),
			"clusterRef name must not be empty",
		))
	}

	// NodeName検証
	if r.Spec.NodeName == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "nodeName"),
			"nodeName must not be empty",
		))
	}

	// Redfish Endpoint検証
	if r.Spec.Redfish.Endpoint == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "redfish", "endpoint"),
			"endpoint must not be empty",
		))
	} else {
		parsedURL, err := url.ParseRequestURI(r.Spec.Redfish.Endpoint)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "redfish", "endpoint"),
				r.Spec.Redfish.Endpoint,
				"endpoint must be a valid HTTP or HTTPS URL",
			))
		}
	}

	// Redfish CredentialsSecretRef検証
	if r.Spec.Redfish.CredentialsSecretRef.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "redfish", "credentialsSecretRef", "name"),
			"credentialsSecretRef name must not be empty",
		))
	}

	// Redfish Power Shutdown Timeout検証
	if r.Spec.Redfish.Power.Shutdown.Timeout != nil && r.Spec.Redfish.Power.Shutdown.Timeout.Duration <= 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "redfish", "power", "shutdown", "timeout"),
			r.Spec.Redfish.Power.Shutdown.Timeout.Duration.String(),
			"timeout must be greater than 0",
		))
	}

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: GroupVersion.Group, Kind: "RunnerMachine"},
		r.Name,
		allErrs,
	)
}
