package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

const (
	// FieldManagerNameはServer-Side Apply時に使用するフィールドマネージャー名
	FieldManagerName = "smart-hibernatable-actions-runner-controller"
)

// controllerReferenceは指定されたオーナーオブジェクトに対するOwnerReferenceApplyConfigurationを生成する
func controllerReference(owner client.Object, scheme *runtime.Scheme) (*metav1apply.OwnerReferenceApplyConfiguration, error) {
	gvk, err := apiutil.GVKForObject(owner, scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to get GVK for object: %w", err)
	}
	return metav1apply.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(owner.GetName()).
		WithUID(owner.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true), nil
}

// applyResourceはApplyConfigurationをunstructuredに変換してServer-Side Applyを実行する
func applyResource(ctx context.Context, c client.Client, applyConfig any) error {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(applyConfig)
	if err != nil {
		return fmt.Errorf("failed to convert apply configuration to unstructured: %w", err)
	}

	patch := &unstructured.Unstructured{
		Object: obj,
	}

	return c.Patch(ctx, patch, client.Apply, &client.PatchOptions{
		FieldManager: FieldManagerName,
		Force:        new(true),
	})
}
