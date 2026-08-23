package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/actions/scaleset"
	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/runner"
)

func TestEphemeralRunnerReconciler_WaitingForCluster(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default"},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseStarting,
			APIReachable: false,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 0,
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-app-secret", Namespace: "default"},
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0fake\n-----END RSA PRIVATE KEY-----"),
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ss1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "github-app-secret"},
			},
		},
	}

	epRunner := &ghav1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "ss1-runner-1",
			Namespace:  "default",
			Finalizers: []string{runner.FinalizerRunnerCleanup},
		},
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "ss1-runner-1",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhasePending,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, nodePool, secret, scaleSet, epRunner).
		WithStatusSubresource(epRunner).
		Build()

	r := &EphemeralRunnerReconciler{
		Client: fakeClient,
		Scheme: scheme,
		ScaleSetFactory: &fakeScaleSetFactory{
			fakeClient: &fakeScaleSetClient{scaleSetID: 100},
		},
		RemoteProvider: &fakeRemoteProvider{},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updated ghav1alpha1.EphemeralRunner
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "ss1-runner-1"}, &updated); err != nil {
		t.Fatalf("failed to get runner: %v", err)
	}

	if updated.Status.Phase != ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster {
		t.Errorf("expected phase %s, got %s", ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster, updated.Status.Phase)
	}
}

func TestEphemeralRunnerReconciler_Provisioning(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-app-secret", Namespace: "default"},
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0fake\n-----END RSA PRIVATE KEY-----"),
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ss1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "github-app-secret"},
			},
			Runner: ghav1alpha1.RunnerTemplateSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "runner", Image: "runner:latest"},
						},
					},
				},
			},
		},
		Status: ghav1alpha1.RunnerScaleSetStatus{
			ScaleSetID: 100,
		},
	}

	epRunner := &ghav1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "ss1-runner-1",
			Namespace:  "default",
			Finalizers: []string{runner.FinalizerRunnerCleanup},
		},
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "ss1-runner-1",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseWaitingForCluster,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, nodePool, secret, scaleSet, epRunner).
		WithStatusSubresource(epRunner).
		Build()

	remoteClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	r := &EphemeralRunnerReconciler{
		Client: fakeClient,
		Scheme: scheme,
		ScaleSetFactory: &fakeScaleSetFactory{
			fakeClient: &fakeScaleSetClient{scaleSetID: 100},
		},
		RemoteProvider: &fakeRemoteProvider{
			client: remoteClient,
		},
	}

	// 1回目のReconcile: Attemptの事前永続化
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2回目のReconcile: JIT生成とリモートPod/Secret作成
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Remote cluster上にPodとSecretが作成されているか確認
	var pod corev1.Pod
	if err := remoteClient.Get(context.Background(), client.ObjectKey{Namespace: "gha-runners", Name: "ss1-runner-1"}, &pod); err != nil {
		t.Fatalf("expected remote pod to be created: %v", err)
	}

	var jitSecret corev1.Secret
	if err := remoteClient.Get(context.Background(), client.ObjectKey{Namespace: "gha-runners", Name: "ss1-runner-1-jit"}, &jitSecret); err != nil {
		t.Fatalf("expected remote JIT secret to be created: %v", err)
	}
}

func TestEphemeralRunnerReconciler_OrphanRunnerRecovery(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-app-secret", Namespace: "default"},
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0fake\n-----END RSA PRIVATE KEY-----"),
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ss1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "github-app-secret"},
			},
			Runner: ghav1alpha1.RunnerTemplateSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "runner", Image: "runner:latest"},
						},
					},
				},
			},
		},
		Status: ghav1alpha1.RunnerScaleSetStatus{
			ScaleSetID: 100,
		},
	}

	now := metav1.Now()
	epRunner := &ghav1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "ss1-runner-orphan",
			Namespace:  "default",
			Finalizers: []string{runner.FinalizerRunnerCleanup},
		},
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "ss1-runner-orphan",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseProvisioning,
			Provisioning: &ghav1alpha1.ProvisioningAttemptStatus{
				ID:         "att-1",
				RunnerName: "ss1-runner-orphan",
				StartedAt:  &now,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, nodePool, secret, scaleSet, epRunner).
		WithStatusSubresource(epRunner).
		Build()

	remoteClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	scaleSetClient := &fakeScaleSetClient{
		scaleSetID: 100,
		existingRunnerRef: &scaleset.RunnerReference{
			ID:   777,
			Name: "ss1-runner-orphan",
		},
	}

	r := &EphemeralRunnerReconciler{
		Client: fakeClient,
		Scheme: scheme,
		ScaleSetFactory: &fakeScaleSetFactory{
			fakeClient: scaleSetClient,
		},
		RemoteProvider: &fakeRemoteProvider{
			client: remoteClient,
		},
	}

	// Reconcile: GitHub上の孤立Runner(ID:777)がRemoveRunnerされ、新しくJITが生成されること
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-orphan"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(scaleSetClient.removedRunnerIDs) == 0 || scaleSetClient.removedRunnerIDs[0] != 777 {
		t.Errorf("expected existing runner ID 777 to be removed before creating new JIT config, got %v", scaleSetClient.removedRunnerIDs)
	}
}

func TestEphemeralRunnerReconciler_IdempotentProvisioningWithExistingSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-app-secret", Namespace: "default"},
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0fake\n-----END RSA PRIVATE KEY-----"),
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ss1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "github-app-secret"},
			},
			Runner: ghav1alpha1.RunnerTemplateSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: "runner", Image: "runner:latest"},
						},
					},
				},
			},
		},
		Status: ghav1alpha1.RunnerScaleSetStatus{
			ScaleSetID: 100,
		},
	}

	epRunner := &ghav1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "ss1-runner-1",
			Namespace:  "default",
			UID:        "runner-uid-1",
			Finalizers: []string{runner.FinalizerRunnerCleanup},
		},
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "ss1-runner-1",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseProvisioning,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, nodePool, secret, scaleSet, epRunner).
		WithStatusSubresource(epRunner).
		Build()

	// 既にSecretが存在するケース（前回のReconcileでSecret作成後にクラッシュした場合の再現）
	existingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ss1-runner-1-jit",
			Namespace: "gha-runners",
			Labels: map[string]string{
				runner.LabelRunnerUID: "runner-uid-1",
			},
		},
		Data: map[string][]byte{
			runner.JitConfigSecretKey: []byte("existing-jit-config"),
		},
	}

	remoteClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existingSecret).
		Build()

	scaleSetClient := &fakeScaleSetClient{scaleSetID: 100}
	r := &EphemeralRunnerReconciler{
		Client: fakeClient,
		Scheme: scheme,
		ScaleSetFactory: &fakeScaleSetFactory{
			fakeClient: scaleSetClient,
		},
		RemoteProvider: &fakeRemoteProvider{
			client: remoteClient,
		},
	}

	// 1回目のReconcile: Attempt初期化
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2回目のReconcile: 既存Secretの再利用とPod作成
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 既存Secretが再利用され、Podが作成されてPhaseがStartingに遷移すること
	var pod corev1.Pod
	if err := remoteClient.Get(context.Background(), client.ObjectKey{Namespace: "gha-runners", Name: "ss1-runner-1"}, &pod); err != nil {
		t.Fatalf("expected remote pod to be created: %v", err)
	}

	var updatedRunner ghav1alpha1.EphemeralRunner
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "ss1-runner-1"}, &updatedRunner); err != nil {
		t.Fatalf("failed to get runner: %v", err)
	}
	if updatedRunner.Status.Phase != ghav1alpha1.EphemeralRunnerPhaseStarting {
		t.Errorf("expected phase %s, got %s", ghav1alpha1.EphemeralRunnerPhaseStarting, updatedRunner.Status.Phase)
	}
}

func TestEphemeralRunnerReconciler_PodFailedSnapshotAndTTL(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ss1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
	}

	epRunner := &ghav1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "ss1-runner-failed",
			Namespace:  "default",
			Finalizers: []string{runner.FinalizerRunnerCleanup},
		},
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "ss1-runner-failed",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseStarting,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, nodePool, scaleSet, epRunner).
		WithStatusSubresource(epRunner).
		Build()

	failedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ss1-runner-failed",
			Namespace: "gha-runners",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "runner",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
							Message:  "Container exceeded memory limit",
						},
					},
				},
			},
		},
	}

	remoteClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(failedPod).
		Build()

	r := &EphemeralRunnerReconciler{
		Client:         fakeClient,
		Scheme:         scheme,
		RemoteProvider: &fakeRemoteProvider{client: remoteClient},
	}

	// 1回目のReconcile: PodFailedを検知してStatus.Failureを記録し、リモートリソースを即削除、CRは保持
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-failed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updatedRunner ghav1alpha1.EphemeralRunner
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "ss1-runner-failed"}, &updatedRunner); err != nil {
		t.Fatalf("expected CR to be retained: %v", err)
	}

	if updatedRunner.Status.Phase != ghav1alpha1.EphemeralRunnerPhaseFailed {
		t.Errorf("expected phase Failed, got %s", updatedRunner.Status.Phase)
	}
	if updatedRunner.Status.Failure == nil {
		t.Fatal("expected Failure status to be populated")
	}
	if updatedRunner.Status.Failure.Reason != "OOMKilled" || updatedRunner.Status.Failure.ExitCode != 137 {
		t.Errorf("expected OOMKilled with exitCode 137, got %s / %d", updatedRunner.Status.Failure.Reason, updatedRunner.Status.Failure.ExitCode)
	}

	// リモートPodが削除されていることを確認
	var podCheck corev1.Pod
	err = remoteClient.Get(context.Background(), client.ObjectKey{Namespace: "gha-runners", Name: "ss1-runner-failed"}, &podCheck)
	if err == nil {
		t.Errorf("expected remote pod to be immediately deleted")
	}

	// 2回目のReconcile (TTL経過後): 1時間以上経過したFinishedAtを設定してReconcile (Deleteが発行される)
	past := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	updatedRunner.Status.FinishedAt = &past
	_ = fakeClient.Status().Update(context.Background(), &updatedRunner)

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-failed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3回目のReconcile: DeletionTimestampが付いたオブジェクトのFinalizer除去が完了して完全に削除される
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-failed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// CRが削除されていることを確認
	var finalRunner ghav1alpha1.EphemeralRunner
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "ss1-runner-failed"}, &finalRunner)
	if err == nil {
		t.Errorf("expected expired Failed runner CR to be deleted")
	}
}

func TestEphemeralRunnerReconciler_PodCompletedTTL(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ss1", Namespace: "default"},
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
	}

	epRunner := &ghav1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "ss1-runner-completed",
			Namespace:  "default",
			Finalizers: []string{runner.FinalizerRunnerCleanup},
		},
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "ss1-runner-completed",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseStarting,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, nodePool, scaleSet, epRunner).
		WithStatusSubresource(epRunner).
		Build()

	completedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ss1-runner-completed",
			Namespace: "gha-runners",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
		},
	}

	remoteClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(completedPod).
		Build()

	r := &EphemeralRunnerReconciler{
		Client:         fakeClient,
		Scheme:         scheme,
		RemoteProvider: &fakeRemoteProvider{client: remoteClient},
	}

	// 1回目のReconcile: PodSucceededを検知してCompletedに遷移し、リモートリソースを削除、CRはTTL保持
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-completed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updatedRunner ghav1alpha1.EphemeralRunner
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "ss1-runner-completed"}, &updatedRunner); err != nil {
		t.Fatalf("expected Completed CR to be retained: %v", err)
	}

	if updatedRunner.Status.Phase != ghav1alpha1.EphemeralRunnerPhaseCompleted {
		t.Errorf("expected phase Completed, got %s", updatedRunner.Status.Phase)
	}
	if updatedRunner.Status.FinishedAt == nil {
		t.Fatal("expected FinishedAt to be populated")
	}

	// 2回目のReconcile (10分経過後): Deleteが発行される
	past := metav1.NewTime(time.Now().Add(-15 * time.Minute))
	updatedRunner.Status.FinishedAt = &past
	_ = fakeClient.Status().Update(context.Background(), &updatedRunner)

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-completed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3回目のReconcile: DeletionTimestampが付いたオブジェクトのFinalizer除去が完了して完全に削除される
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: "default", Name: "ss1-runner-completed"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var finalRunner ghav1alpha1.EphemeralRunner
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "ss1-runner-completed"}, &finalRunner)
	if err == nil {
		t.Errorf("expected expired Completed runner CR to be deleted")
	}
}
