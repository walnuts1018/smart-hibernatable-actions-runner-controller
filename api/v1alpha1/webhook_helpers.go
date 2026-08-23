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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	defaultRunnerNamespace = "gha-runners"
)

func validateImmutableString(allErrs *field.ErrorList, path *field.Path, oldVal, newVal string) {
	if oldVal != newVal {
		*allErrs = append(*allErrs, field.Forbidden(path, "field is immutable once created"))
	}
}

func appendStatusErrorCauses(allErrs *field.ErrorList, err error) error {
	if err == nil {
		return nil
	}
	if statusErr, ok := err.(*apierrors.StatusError); ok {
		for _, detail := range statusErr.ErrStatus.Details.Causes {
			*allErrs = append(*allErrs, field.Invalid(field.NewPath(detail.Field), "", detail.Message))
		}
		return nil
	}
	return err
}
