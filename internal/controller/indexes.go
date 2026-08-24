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

	// IndexMachineNodePoolRefNameはRunnerMachineのspec.nodePoolRef.nameのインデックスキーです
	IndexMachineNodePoolRefName = ".spec.nodePoolRef.name"

	// IndexScaleSetRefNameはEphemeralRunner / EphemeralRunnerSetのspec.scaleSetRef.nameのインデックスキーです
	IndexScaleSetRefName = ".spec.scaleSetRef.name"

	// IndexClusterRefNameはRunnerMachineのspec.clusterRef.nameのインデックスキーです
	IndexClusterRefName = ".spec.clusterRef.name"

	// IndexGitHubRunnerNameはEphemeralRunnerのGitHub RunnerNameのインデックスキーです
	IndexGitHubRunnerName = ".status.provisioning.runnerName"
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

	// 2. RunnerMachine -> NodePoolRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &ghav1alpha1.RunnerMachine{}, IndexMachineNodePoolRefName, func(obj client.Object) []string {
		rm, ok := obj.(*ghav1alpha1.RunnerMachine)
		if !ok || rm.Spec.NodePoolRef == nil || rm.Spec.NodePoolRef.Name == "" {
			return nil
		}
		return []string{rm.Spec.NodePoolRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to setup index for RunnerMachine %s: %w", IndexMachineNodePoolRefName, err)
	}

	// 3. EphemeralRunnerSet -> ScaleSetRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &ghav1alpha1.EphemeralRunnerSet{}, IndexScaleSetRefName, func(obj client.Object) []string {
		ers, ok := obj.(*ghav1alpha1.EphemeralRunnerSet)
		if !ok || ers.Spec.ScaleSetRef.Name == "" {
			return nil
		}
		return []string{ers.Spec.ScaleSetRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to setup index for EphemeralRunnerSet %s: %w", IndexScaleSetRefName, err)
	}

	// 4. EphemeralRunner -> ScaleSetRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &ghav1alpha1.EphemeralRunner{}, IndexScaleSetRefName, func(obj client.Object) []string {
		er, ok := obj.(*ghav1alpha1.EphemeralRunner)
		if !ok || er.Spec.ScaleSetRef.Name == "" {
			return nil
		}
		return []string{er.Spec.ScaleSetRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to setup index for EphemeralRunner %s: %w", IndexScaleSetRefName, err)
	}

	// 5. RunnerMachine -> ClusterRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &ghav1alpha1.RunnerMachine{}, IndexClusterRefName, func(obj client.Object) []string {
		rm, ok := obj.(*ghav1alpha1.RunnerMachine)
		if !ok || rm.Spec.ClusterRef.Name == "" {
			return nil
		}
		return []string{rm.Spec.ClusterRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to setup index for RunnerMachine %s: %w", IndexClusterRefName, err)
	}

	// 6. EphemeralRunner -> GitHub RunnerName (for Listener reverse lookup)
	if err := mgr.GetFieldIndexer().IndexField(ctx, &ghav1alpha1.EphemeralRunner{}, IndexGitHubRunnerName, func(obj client.Object) []string {
		er, ok := obj.(*ghav1alpha1.EphemeralRunner)
		if !ok {
			return nil
		}
		if er.Status.Provisioning != nil && er.Status.Provisioning.RunnerName != "" {
			return []string{er.Status.Provisioning.RunnerName}
		}
		if er.Spec.RunnerName != "" {
			return []string{er.Spec.RunnerName}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to setup index for EphemeralRunner %s: %w", IndexGitHubRunnerName, err)
	}

	return nil
}

// listWithIndexFallback lists objects using an index field, falling back to a full namespace list if indexing is unavailable.
func listWithIndexFallback(ctx context.Context, c client.Reader, list client.ObjectList, namespace, indexField, indexValue string) error {
	if err := c.List(ctx, list, client.InNamespace(namespace), client.MatchingFields{
		indexField: indexValue,
	}); err != nil {
		return c.List(ctx, list, client.InNamespace(namespace))
	}
	return nil
}
