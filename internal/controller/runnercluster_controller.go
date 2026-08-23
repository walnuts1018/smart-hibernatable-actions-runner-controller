package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/conditions"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/remotecluster"
)

// RunnerClusterReconciler reconciles a RunnerCluster object.
type RunnerClusterReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	RemoteProvider remotecluster.Provider
}

// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnerclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnerclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnerclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=gha.walnuts.dev,resources=runnermachines,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *RunnerClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cluster ghav1alpha1.RunnerCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			r.RemoteProvider.InvalidateCache(fmt.Sprintf("%s/%s", req.Namespace, req.Name))
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	origCluster := cluster.DeepCopy()

	// 1. クラスタに属するMachine一覧を確認して電源状態を集計
	var machineList ghav1alpha1.RunnerMachineList
	if err := r.List(ctx, &machineList, client.InNamespace(cluster.Namespace), client.MatchingFields{
		IndexClusterRefName: cluster.Name,
	}); err != nil {
		// インデックスが未登録の場合はフォールバック
		if err := r.List(ctx, &machineList, client.InNamespace(cluster.Namespace)); err != nil {
			log.Error(err, "failed to list runner machines")
			return ctrl.Result{}, err
		}
	}

	var (
		hasOnMachine            bool
		hasTransitioningMachine bool
		hasClusterMachine       bool
	)

	for _, m := range machineList.Items {
		if m.Spec.ClusterRef.Name == cluster.Name {
			hasClusterMachine = true
			switch m.Status.PowerState {
			case ghav1alpha1.PowerStateOn:
				hasOnMachine = true
			case ghav1alpha1.PowerStatePoweringOn, ghav1alpha1.PowerStatePoweringOff:
				hasTransitioningMachine = true
			}
		}
	}

	oldPhase := cluster.Status.Phase

	// 2. ショートサーキット判定: 全マシンが電源OFFの場合はAPIヘルスチェックをスキップ
	if hasClusterMachine && !hasOnMachine && !hasTransitioningMachine {
		cluster.Status.APIReachable = false
		cluster.Status.Phase = ghav1alpha1.RunnerClusterPhaseOffline
		conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeKubeconfigValid, metav1.ConditionTrue, conditions.ReasonSuccess, "Kubeconfig is present")
		conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeAPIReachable, metav1.ConditionFalse, conditions.ReasonPowerStateOff, "All runner machines are off")
		conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonPowerStateOff, "All runner machines are off")
		metrics.ClusterAPIReachable.WithLabelValues(cluster.Namespace, cluster.Name).Set(0)
	} else {
		// 3. マシンが稼働中または起動中の場合のみKubeconfig Secret妥当性とAPI疎通確認を実施
		err := r.RemoteProvider.CheckHealth(ctx, &cluster)
		if err != nil {
			if r.Recorder != nil && cluster.Status.APIReachable {
				r.Recorder.Eventf(&cluster, corev1.EventTypeWarning, "APIUnreachable", "Runner cluster API unreachable: %v", err)
			}
			conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeKubeconfigValid, metav1.ConditionFalse, conditions.ReasonInvalidKubeconfig, err.Error())
			conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeAPIReachable, metav1.ConditionFalse, conditions.ReasonAPIUnreachable, err.Error())
			cluster.Status.APIReachable = false
			metrics.ClusterAPIReachable.WithLabelValues(cluster.Namespace, cluster.Name).Set(0)
		} else {
			if r.Recorder != nil && !cluster.Status.APIReachable {
				r.Recorder.Eventf(&cluster, corev1.EventTypeNormal, "ClusterAPIReachable", "Runner cluster Kubernetes API is reachable")
			}
			conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeKubeconfigValid, metav1.ConditionTrue, conditions.ReasonSuccess, "Kubeconfig is valid")
			conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeAPIReachable, metav1.ConditionTrue, conditions.ReasonAPISucceeded, "Runner cluster Kubernetes API is reachable")
			cluster.Status.APIReachable = true
			metrics.ClusterAPIReachable.WithLabelValues(cluster.Namespace, cluster.Name).Set(1)

			// Cluster Identity (kube-system UID) の検証
			clusterUID, uidErr := r.RemoteProvider.GetClusterUID(ctx, &cluster)
			if uidErr != nil {
				log.Error(uidErr, "failed to get cluster identity UID from remote cluster")
			} else if cluster.Status.ClusterUID == "" {
				cluster.Status.ClusterUID = clusterUID
			} else if cluster.Status.ClusterUID != clusterUID {
				log.Error(fmt.Errorf("cluster identity mismatch"), "remote cluster UID changed (expected %s, got %s)", cluster.Status.ClusterUID, clusterUID)
				if r.Recorder != nil {
					r.Recorder.Eventf(&cluster, corev1.EventTypeWarning, "ClusterIdentityMismatch", "Remote cluster UID changed: expected %s, got %s", cluster.Status.ClusterUID, clusterUID)
				}
				conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonClusterIdentityMismatch, "Remote cluster identity mismatch; stopping mutation")
				cluster.Status.APIReachable = false
				cluster.Status.Phase = ghav1alpha1.RunnerClusterPhaseDegraded
			}
		}

		if cluster.Status.APIReachable {
			cluster.Status.Phase = ghav1alpha1.RunnerClusterPhaseReady
			conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeReady, metav1.ConditionTrue, conditions.ReasonReady, "Runner cluster is ready")
		} else if hasTransitioningMachine {
			cluster.Status.Phase = ghav1alpha1.RunnerClusterPhaseStarting
			conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonPowerTransitioning, "Runner machines are starting or waiting for API readiness")
		} else {
			cluster.Status.Phase = ghav1alpha1.RunnerClusterPhaseDegraded
			conditions.SetCondition(&cluster.Status.Conditions, conditions.TypeReady, metav1.ConditionFalse, conditions.ReasonNotReady, "Runner cluster is degraded (machine is on but API is unreachable)")
		}
	}

	if r.Recorder != nil && oldPhase != cluster.Status.Phase {
		switch cluster.Status.Phase {
		case ghav1alpha1.RunnerClusterPhaseReady:
			r.Recorder.Eventf(&cluster, corev1.EventTypeNormal, "ClusterReady", "Runner cluster is Ready")
		case ghav1alpha1.RunnerClusterPhaseOffline:
			r.Recorder.Eventf(&cluster, corev1.EventTypeNormal, "ClusterOffline", "Runner cluster is Offline")
		case ghav1alpha1.RunnerClusterPhaseDegraded:
			r.Recorder.Eventf(&cluster, corev1.EventTypeWarning, "ClusterDegraded", "Runner cluster is Degraded")
		}
	}

	// Status更新
	if err := r.updateStatus(ctx, &cluster, origCluster); err != nil {
		log.Error(err, "failed to patch runner cluster status")
		return ctrl.Result{}, err
	}

	// Phaseに応じたRequeue間隔
	requeueAfter := 30 * time.Second
	switch cluster.Status.Phase {
	case ghav1alpha1.RunnerClusterPhaseStarting:
		requeueAfter = 10 * time.Second
	case ghav1alpha1.RunnerClusterPhaseOffline:
		requeueAfter = 1 * time.Minute
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *RunnerClusterReconciler) updateStatus(ctx context.Context, cluster, orig *ghav1alpha1.RunnerCluster) error {
	return r.Status().Patch(ctx, cluster, client.MergeFrom(orig))
}

func (r *RunnerClusterReconciler) findClustersForMachine(ctx context.Context, obj client.Object) []ctrl.Request {
	m, ok := obj.(*ghav1alpha1.RunnerMachine)
	if !ok {
		return nil
	}

	return []ctrl.Request{
		{
			Namespace: m.Namespace,
			Name:      m.Spec.ClusterRef.Name,
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *RunnerClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ghav1alpha1.RunnerCluster{}).
		Watches(&ghav1alpha1.RunnerMachine{}, handler.EnqueueRequestsFromMapFunc(r.findClustersForMachine)).
		Complete(r)
}
