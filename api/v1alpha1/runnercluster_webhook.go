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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	validationutil "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// runnerclusterlog is for logging in this package.
var runnerclusterlog = logf.Log.WithName("runnercluster-resource")

// SetupWebhookWithManager registers the webhook with the manager.
func (r *RunnerCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithDefaulter(r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-gha-walnuts-dev-v1alpha1-runnercluster,mutating=true,failurePolicy=fail,sideEffects=None,groups=gha.walnuts.dev,resources=runnerclusters,verbs=create;update,versions=v1alpha1,name=mrunnercluster.kb.io,admissionReviewVersions=v1

var _ admission.Defaulter[*RunnerCluster] = &RunnerCluster{}

// Default implements admission.Defaulter so a webhook will be registered for the type
func (r *RunnerCluster) Default(ctx context.Context, obj *RunnerCluster) error {
	runnerclusterlog.Info("defaulting RunnerCluster", "name", obj.Name)

	if obj.Spec.RunnerNamespace == "" {
		obj.Spec.RunnerNamespace = "gha-runners"
	}
	if obj.Spec.Readiness.APIRequestTimeout == nil {
		obj.Spec.Readiness.APIRequestTimeout = &metav1.Duration{Duration: 5 * time.Second}
	}
	if obj.Spec.Readiness.NodeReadyTimeout == nil {
		obj.Spec.Readiness.NodeReadyTimeout = &metav1.Duration{Duration: 10 * time.Minute}
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-gha-walnuts-dev-v1alpha1-runnercluster,mutating=false,failurePolicy=fail,sideEffects=None,groups=gha.walnuts.dev,resources=runnerclusters,verbs=create;update,versions=v1alpha1,name=vrunnercluster.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*RunnerCluster] = &RunnerCluster{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type
func (r *RunnerCluster) ValidateCreate(ctx context.Context, obj *RunnerCluster) (admission.Warnings, error) {
	runnerclusterlog.Info("validate create RunnerCluster", "name", obj.Name)
	return nil, obj.validateRunnerCluster()
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type
func (r *RunnerCluster) ValidateUpdate(ctx context.Context, oldObj, newObj *RunnerCluster) (admission.Warnings, error) {
	runnerclusterlog.Info("validate update RunnerCluster", "name", newObj.Name)

	var allErrs field.ErrorList

	// 不変フィールドの検証
	if newObj.Spec.RunnerNamespace != oldObj.Spec.RunnerNamespace {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "runnerNamespace"),
			"field is immutable once created",
		))
	}

	if err := newObj.validateRunnerCluster(); err != nil {
		if statusErr, ok := err.(*apierrors.StatusError); ok {
			for _, detail := range statusErr.ErrStatus.Details.Causes {
				allErrs = append(allErrs, field.Invalid(field.NewPath(detail.Field), "", detail.Message))
			}
		} else {
			return nil, err
		}
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "RunnerCluster"},
			newObj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type
func (r *RunnerCluster) ValidateDelete(ctx context.Context, obj *RunnerCluster) (admission.Warnings, error) {
	runnerclusterlog.Info("validate delete RunnerCluster", "name", obj.Name)
	return nil, nil
}

func (r *RunnerCluster) validateRunnerCluster() error {
	var allErrs field.ErrorList

	// KubeconfigSecretRef検証
	if r.Spec.KubeconfigSecretRef.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "kubeconfigSecretRef", "name"),
			"kubeconfigSecretRef name must not be empty",
		))
	}
	if r.Spec.KubeconfigSecretRef.Key == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "kubeconfigSecretRef", "key"),
			"kubeconfigSecretRef key must not be empty",
		))
	}

	// RunnerNamespace検証
	if r.Spec.RunnerNamespace != "" {
		if errs := validationutil.IsDNS1123Label(r.Spec.RunnerNamespace); len(errs) > 0 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "runnerNamespace"),
				r.Spec.RunnerNamespace,
				fmt.Sprintf("runnerNamespace must be a valid DNS-1123 label: %v", errs),
			))
		}
	}

	// Readinessタイムアウト検証
	if r.Spec.Readiness.APIRequestTimeout != nil && r.Spec.Readiness.APIRequestTimeout.Duration <= 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "readiness", "apiRequestTimeout"),
			r.Spec.Readiness.APIRequestTimeout.Duration.String(),
			"apiRequestTimeout must be greater than 0",
		))
	}
	if r.Spec.Readiness.NodeReadyTimeout != nil && r.Spec.Readiness.NodeReadyTimeout.Duration <= 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "readiness", "nodeReadyTimeout"),
			r.Spec.Readiness.NodeReadyTimeout.Duration.String(),
			"nodeReadyTimeout must be greater than 0",
		))
	}

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: GroupVersion.Group, Kind: "RunnerCluster"},
		r.Name,
		allErrs,
	)
}
