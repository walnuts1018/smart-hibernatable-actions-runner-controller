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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// runnernodepoollog is for logging in this package.
var runnernodepoollog = logf.Log.WithName("runnernodepool-resource")

// SetupWebhookWithManager registers the webhook with the manager.
func (r *RunnerNodePool) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithDefaulter(r).
		WithValidator(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-sharc.walnuts.dev-v1alpha1-runnernodepool,mutating=true,failurePolicy=fail,sideEffects=None,groups=sharc.walnuts.dev,resources=runnernodepools,verbs=create;update,versions=v1alpha1,name=mrunnernodepool.kb.io,admissionReviewVersions=v1

var _ admission.Defaulter[*RunnerNodePool] = &RunnerNodePool{}

// Default implements admission.Defaulter so a webhook will be registered for the type
func (r *RunnerNodePool) Default(_ context.Context, obj *RunnerNodePool) error {
	runnernodepoollog.Info("defaulting RunnerNodePool", "name", obj.Name)

	if obj.Spec.Scaling.MinNodes < 0 {
		obj.Spec.Scaling.MinNodes = 0
	}
	if obj.Spec.Scaling.ScaleDownDelay == nil {
		obj.Spec.Scaling.ScaleDownDelay = &metav1.Duration{Duration: 10 * time.Minute}
	}
	if obj.Spec.Drain == nil {
		obj.Spec.Drain = &NodePoolDrainSpec{
			Timeout: &metav1.Duration{Duration: 10 * time.Minute},
		}
	} else if obj.Spec.Drain.Timeout == nil {
		obj.Spec.Drain.Timeout = &metav1.Duration{Duration: 10 * time.Minute}
	}
	return nil
}

// +kubebuilder:webhook:path=/validate-sharc.walnuts.dev-v1alpha1-runnernodepool,mutating=false,failurePolicy=fail,sideEffects=None,groups=sharc.walnuts.dev,resources=runnernodepools,verbs=create;update,versions=v1alpha1,name=vrunnernodepool.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*RunnerNodePool] = &RunnerNodePool{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type
func (r *RunnerNodePool) ValidateCreate(_ context.Context, obj *RunnerNodePool) (admission.Warnings, error) {
	runnernodepoollog.Info("validate create RunnerNodePool", "name", obj.Name)
	return nil, obj.validateRunnerNodePool()
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type
func (r *RunnerNodePool) ValidateUpdate(_ context.Context, oldObj, newObj *RunnerNodePool) (admission.Warnings, error) {
	runnernodepoollog.Info("validate update RunnerNodePool", "name", newObj.Name)

	// 削除中のオブジェクト更新（Finalizer 削除など）は検証をスキップして Finalizer デッドロックを防止
	if newObj.DeletionTimestamp != nil {
		return nil, nil
	}

	var allErrs field.ErrorList

	// 不変フィールドの検証
	if newObj.Spec.ClusterRef.Name != oldObj.Spec.ClusterRef.Name {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("spec", "clusterRef", "name"),
			"field is immutable once created",
		))
	}

	if err := appendStatusErrorCauses(&allErrs, newObj.validateRunnerNodePool()); err != nil {
		return nil, err
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "RunnerNodePool"},
			newObj.Name,
			allErrs,
		)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type
func (r *RunnerNodePool) ValidateDelete(_ context.Context, obj *RunnerNodePool) (admission.Warnings, error) {
	runnernodepoollog.Info("validate delete RunnerNodePool", "name", obj.Name)
	return nil, nil
}

func (r *RunnerNodePool) validateRunnerNodePool() error {
	var allErrs field.ErrorList

	// ClusterRef検証
	if r.Spec.ClusterRef.Name == "" {
		allErrs = append(allErrs, field.Required(
			field.NewPath("spec", "clusterRef", "name"),
			"clusterRef name must not be empty",
		))
	}

	// Scaling検証
	if r.Spec.Scaling.MinNodes < 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "scaling", "minNodes"),
			r.Spec.Scaling.MinNodes,
			"minNodes must be greater than or equal to 0",
		))
	}

	if r.Spec.Scaling.MaxNodes != nil {
		if *r.Spec.Scaling.MaxNodes < 1 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "scaling", "maxNodes"),
				*r.Spec.Scaling.MaxNodes,
				"maxNodes must be greater than or equal to 1",
			))
		}

		if r.Spec.Scaling.MinNodes > *r.Spec.Scaling.MaxNodes {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec", "scaling", "minNodes"),
				r.Spec.Scaling.MinNodes,
				"minNodes must be less than or equal to maxNodes",
			))
		}
	}

	if r.Spec.Scaling.ScaleDownDelay != nil && r.Spec.Scaling.ScaleDownDelay.Duration <= 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "scaling", "scaleDownDelay"),
			r.Spec.Scaling.ScaleDownDelay.Duration.String(),
			"scaleDownDelay must be greater than 0",
		))
	}

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: GroupVersion.Group, Kind: "RunnerNodePool"},
		r.Name,
		allErrs,
	)
}
