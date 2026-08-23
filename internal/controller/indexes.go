package controller

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
)

const (
	// IndexNodePoolRefNameはRunnerScaleSetのspec.nodePoolRef.nameのインデックスキーです
	IndexNodePoolRefName = ".spec.nodePoolRef.name"

	// IndexScaleSetRefNameはEphemeralRunnerのspec.scaleSetRef.nameのインデックスキーです
	IndexScaleSetRefName = ".spec.scaleSetRef.name"

	// IndexClusterRefNameはRunnerMachineのspec.clusterRef.nameのインデックスキーです
	IndexClusterRefName = ".spec.clusterRef.name"
)

// SetupIndexesWithManagerはコントローラーマネージャーのインデクサーにリレーションフィールドを登録します
func SetupIndexesWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()

	// 1. RunnerScaleSet -> NodePoolRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &ghav1alpha1.RunnerScaleSet{}, IndexNodePoolRefName, func(obj client.Object) []string {
		ss, ok := obj.(*ghav1alpha1.RunnerScaleSet)
		if !ok || ss.Spec.NodePoolRef.Name == "" {
			return nil
		}
		return []string{ss.Spec.NodePoolRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to setup index for RunnerScaleSet %s: %w", IndexNodePoolRefName, err)
	}

	// 2. EphemeralRunner -> ScaleSetRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &ghav1alpha1.EphemeralRunner{}, IndexScaleSetRefName, func(obj client.Object) []string {
		er, ok := obj.(*ghav1alpha1.EphemeralRunner)
		if !ok || er.Spec.ScaleSetRef.Name == "" {
			return nil
		}
		return []string{er.Spec.ScaleSetRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to setup index for EphemeralRunner %s: %w", IndexScaleSetRefName, err)
	}

	// 3. RunnerMachine -> ClusterRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &ghav1alpha1.RunnerMachine{}, IndexClusterRefName, func(obj client.Object) []string {
		rm, ok := obj.(*ghav1alpha1.RunnerMachine)
		if !ok || rm.Spec.ClusterRef.Name == "" {
			return nil
		}
		return []string{rm.Spec.ClusterRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to setup index for RunnerMachine %s: %w", IndexClusterRefName, err)
	}

	return nil
}
