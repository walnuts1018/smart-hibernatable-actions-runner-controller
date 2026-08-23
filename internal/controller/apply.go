package controller

import (
	"context"
	"fmt"

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

// applyResource executes Server-Side Apply for the given ApplyConfiguration
func applyResource(ctx context.Context, c client.Client, applyConfig runtime.ApplyConfiguration) error {
	return c.Apply(ctx, applyConfig, client.FieldOwner(FieldManagerName), client.ForceOwnership)
}
