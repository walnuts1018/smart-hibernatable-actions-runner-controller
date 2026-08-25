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

const validTestRSAPrivateKeyPEM = `-----BEGIN PRIVATE KEY-----
MIIEvwIBADANBgkqhkiG9w0BAQEFAASCBKkwggSlAgEAAoIBAQC11udPh2QLYPFH
QOvdv7G6yxfheEbBTcj2FsgfqaJGpBb8TlEe0nD3GHe4TJKxdOUbDtOQpbBqFIpX
PVrYUuvFEWlvSvabwWrYbbTnW7yg+YO5HUf4rMmyGt8ZtPvvuymZzBOPk+yfx6Ld
iBIUEuqa1bxk7gahcx+zlxfPqg471Q7zo0BqUy1IgBP2QqPnWZFlP94fg3xbbrGa
LZj1OiAVIZmQu2QaTP75vye/XrTGDD5UtQRg/d/LcAv7iddP68KbT3o0xlryjjzp
n15YqAib0YvWosAde+Z/O2pV6gpn6pvq2trDuC09++SysgWRA9g0IWLWY7X5R1/H
AZDxyaNBAgMBAAECggEAO0yFyk2gtoU6qb3mLT5iO0QX2ZNbn5Y6PuZXBNxQ6zB/
vm/bzG1cIXh9MkDmZbB1NkmzfKxLx4xDQQflJD6GXJG9DGop2clNip7cK8ai0OwN
pMSDv/i5HbfdoYh/0EH84wbGKkBXHhQAbLX/D0TL9QpWkaN9zhC4+dwAC9ytH51i
axShF6CHgydYxNZu/bf5XuNXXlY80LJ+Ud0rd1lXKFbz5fYxgs43RkFg9CWWqqDC
rRweNs8evqzATOrD7REccX/QTUJjgXvRaGlVfiyJQHAUyD7vbY0IqgdWJRlTC3r9
MwJri5QjK+UVdxWGcMdvO++su0mhrpSNnM6II4+68QKBgQDmJrvVtuCzbtAjlvGO
jHQhwOYlysH3gcccwS/Y6RV0Y61DZm7v2s0kp85wtO73iuHnC2KwwXjgi42R0VFr
gAWWkEJ8MddX9LwL62+/bnv2A/ajVWIdY1wUovHRdTx3W3qJWZ+684aC+nFRsgtz
miBBsyfNPmGz2ZLYMC3mbS82FwKBgQDKQx384YUsI5VAk6W/fiw2+W+KmhEpKD2P
t3VHD3zNBUb1wMTReZ8RORDHQ9qZiIEMU+zCRHfyMhd7PzexPYMRzavBPqYsrW5O
Q93n8LZC1H9InWGCC2Elk3WgliGqkHZLCHGF5GrK5G49w1V7m/ms+4zD/HQT8evd
9dY9+DggZwKBgQDYXhnAdUkR51+t1b4KMWkMQnkbll57/XnfQo9k8NvGq967upUY
0S6DA29E7hSqi9qMh1ukqH6nOwtAxvQwiA642a5na8PzYJVY72IDKi9HvbolG6Q9
1KdAj1+fdwP9gfbVIXjVHRScFi5qi2PQrlkc6vzEK51Wo3k13TWJp6P2yQKBgQCo
eWF4K21fB8ChipqMOA+iNwEG5TAYJTGqDTk92JOuvo+N0mTeyzyI/wyPvmBOdNpx
J1LVumxirADNIypDkyYi5TsEeye1nTx9KqCjOujGH/RpytXWmZ3wy7Q17/fY9/3g
oAbXbRzbJY0CGzuP+6rrwJhPA3C40FEUkFpFQgWWTwKBgQCyqjpc74Kvc0TFrZ3d
uOIW8u0YbaViHk0OV+qO1HjUXDFAESwOGJ8nu92Rf2rui4Wbw4JD4s4s9kofQh/e
YbdWk16w6qaQF2QKr0hUHNyFy5027uabeQulxMRBxEt5kfoS9ul9CqSePkoH99WB
83Az7XN4r9nFbKH6NdKHUw6GOQ==
-----END PRIVATE KEY-----`

func TestEphemeralRunnerReconciler_WaitingForCluster(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseStarting,
			APIReachable: false,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 0,
		},
	}

	secret := &corev1.Secret{
		Name: "github-app-secret", Namespace: "default",
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte(validTestRSAPrivateKeyPEM),
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				CredentialsSecretRef: corev1.LocalObjectReference{Name: "github-app-secret"},
			},
		},
	}

	epRunner := &ghav1alpha1.EphemeralRunner{
		Name:       "ss1-runner-1",
		Namespace:  "default",
		Finalizers: []string{runner.FinalizerRunnerCleanup},
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
		Namespace: "default", Name: "ss1-runner-1",
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
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	secret := &corev1.Secret{
		Name: "github-app-secret", Namespace: "default",
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte(validTestRSAPrivateKeyPEM),
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
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
		Name:       "ss1-runner-1",
		Namespace:  "default",
		Finalizers: []string{runner.FinalizerRunnerCleanup},
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
		Namespace: "default", Name: "ss1-runner-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2回目のReconcile: JIT生成とリモートPod/Secret作成
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "ss1-runner-1",
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
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	secret := &corev1.Secret{
		Name: "github-app-secret", Namespace: "default",
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte(validTestRSAPrivateKeyPEM),
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
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
		Name:       "ss1-runner-orphan",
		Namespace:  "default",
		Finalizers: []string{runner.FinalizerRunnerCleanup},
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
		Namespace: "default", Name: "ss1-runner-orphan",
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
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	secret := &corev1.Secret{
		Name: "github-app-secret", Namespace: "default",
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte(validTestRSAPrivateKeyPEM),
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
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
		Name:       "ss1-runner-1",
		Namespace:  "default",
		UID:        "runner-uid-1",
		Finalizers: []string{runner.FinalizerRunnerCleanup},
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
		Name:      "ss1-runner-1-jit",
		Namespace: "gha-runners",
		Labels: map[string]string{
			runner.LabelRunnerUID: "runner-uid-1",
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
		Namespace: "default", Name: "ss1-runner-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2回目のReconcile: 既存Secretの再利用とPod作成
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "ss1-runner-1",
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
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
	}

	epRunner := &ghav1alpha1.EphemeralRunner{
		Name:       "ss1-runner-failed",
		Namespace:  "default",
		Finalizers: []string{runner.FinalizerRunnerCleanup},
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
		Name:      "ss1-runner-failed",
		Namespace: "gha-runners",
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
		Namespace: "default", Name: "ss1-runner-failed",
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

	// 2回目のReconcile (TTL経過後): 1時間以上経過したFinishedAt/GCEligibleAtを設定してReconcile (Deleteが発行される)
	past := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	updatedRunner.Status.FinishedAt = &past
	updatedRunner.Status.GCEligibleAt = &past
	fakeClient.Status().Update(context.Background(), &updatedRunner)

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "ss1-runner-failed",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3回目のReconcile: DeletionTimestampが付いたオブジェクトのFinalizer除去が完了して完全に削除される
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "ss1-runner-failed",
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
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
	}

	epRunner := &ghav1alpha1.EphemeralRunner{
		Name:       "ss1-runner-completed",
		Namespace:  "default",
		Finalizers: []string{runner.FinalizerRunnerCleanup},
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
		Name:      "ss1-runner-completed",
		Namespace: "gha-runners",
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
		Namespace: "default", Name: "ss1-runner-completed",
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
	updatedRunner.Status.GCEligibleAt = &past
	fakeClient.Status().Update(context.Background(), &updatedRunner)

	_, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "ss1-runner-completed",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3回目のReconcile: DeletionTimestampが付いたオブジェクトのFinalizer除去が完了して完全に削除される
	_, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "ss1-runner-completed",
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

func TestEphemeralRunnerReconciler_PodScheduledWithEmptyReason(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
		Status: ghav1alpha1.RunnerNodePoolStatus{
			ReadyNodes: 1,
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
	}

	epRunner := &ghav1alpha1.EphemeralRunner{
		Name:       "ss1-runner-running",
		Namespace:  "default",
		Finalizers: []string{runner.FinalizerRunnerCleanup},
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "ss1-runner-running",
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

	runningPod := &corev1.Pod{
		Name:      "ss1-runner-running",
		Namespace: "gha-runners",
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionTrue,
					Reason: "", // Standard K8s empty reason on success
				},
			},
		},
	}

	remoteClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(runningPod).
		Build()

	r := &EphemeralRunnerReconciler{
		Client:         fakeClient,
		Scheme:         scheme,
		RemoteProvider: &fakeRemoteProvider{client: remoteClient},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "ss1-runner-running",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var updatedRunner ghav1alpha1.EphemeralRunner
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "ss1-runner-running"}, &updatedRunner); err != nil {
		t.Fatalf("failed to get runner: %v", err)
	}

	if updatedRunner.Status.Phase != ghav1alpha1.EphemeralRunnerPhaseIdle {
		t.Errorf("expected phase Idle, got %s", updatedRunner.Status.Phase)
	}

	var foundScheduledCond bool
	for _, cond := range updatedRunner.Status.Conditions {
		if cond.Type == "PodScheduled" {
			foundScheduledCond = true
			if cond.Reason == "" {
				t.Errorf("PodScheduled condition reason must not be empty")
			}
		}
	}
	if !foundScheduledCond {
		t.Errorf("expected PodScheduled condition on EphemeralRunner")
	}
}

func TestEphemeralRunnerReconciler_DeleteBusyRunner_PodNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
	}

	now := metav1.Now()
	epRunner := &ghav1alpha1.EphemeralRunner{
		Name:              "ss1-runner-busy-deleted",
		Namespace:         "default",
		Finalizers:        []string{runner.FinalizerRunnerCleanup},
		DeletionTimestamp: &now,
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "ss1-runner-busy-deleted",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseBusy,
			GitHub: ghav1alpha1.GitHubRunnerStatus{
				RunnerID:          1234,
				CompletedObserved: false,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, nodePool, scaleSet, epRunner).
		WithStatusSubresource(epRunner).
		Build()

	remoteClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build() // Remote pod does not exist (NotFound)

	r := &EphemeralRunnerReconciler{
		Client:         fakeClient,
		Scheme:         scheme,
		RemoteProvider: &fakeRemoteProvider{client: remoteClient},
	}

	// Reconcile: Because remote pod is NotFound, finalizer should be removed immediately without getting stuck in infinite loop
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "ss1-runner-busy-deleted",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("expected immediate completion (RequeueAfter: 0), got %v", res.RequeueAfter)
	}

	var updatedRunner ghav1alpha1.EphemeralRunner
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "ss1-runner-busy-deleted"}, &updatedRunner)
	if err == nil {
		if len(updatedRunner.Finalizers) != 0 {
			t.Errorf("expected finalizers to be empty, got %v", updatedRunner.Finalizers)
		}
	}
}

func TestEphemeralRunnerReconciler_DeleteBusyRunner_PodStillRunning(t *testing.T) {
	scheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(scheme)
	ghav1alpha1.AddToScheme(scheme)

	cluster := &ghav1alpha1.RunnerCluster{
		Name: "c1", Namespace: "default",
		Spec: ghav1alpha1.RunnerClusterSpec{
			RunnerNamespace: "gha-runners",
		},
		Status: ghav1alpha1.RunnerClusterStatus{
			Phase:        ghav1alpha1.RunnerClusterPhaseReady,
			APIReachable: true,
		},
	}

	nodePool := &ghav1alpha1.RunnerNodePool{
		Name: "p1", Namespace: "default",
		Spec: ghav1alpha1.RunnerNodePoolSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c1"},
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Name: "ss1", Namespace: "default",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			NodePoolRef: corev1.LocalObjectReference{Name: "p1"},
		},
	}

	now := metav1.Now()
	epRunner := &ghav1alpha1.EphemeralRunner{
		Name:              "ss1-runner-busy-running",
		Namespace:         "default",
		Finalizers:        []string{runner.FinalizerRunnerCleanup},
		DeletionTimestamp: &now,
		Spec: ghav1alpha1.EphemeralRunnerSpec{
			ScaleSetRef: corev1.LocalObjectReference{Name: "ss1"},
			RunnerName:  "ss1-runner-busy-running",
		},
		Status: ghav1alpha1.EphemeralRunnerStatus{
			Phase: ghav1alpha1.EphemeralRunnerPhaseBusy,
			GitHub: ghav1alpha1.GitHubRunnerStatus{
				RunnerID:          1234,
				CompletedObserved: false,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, nodePool, scaleSet, epRunner).
		WithStatusSubresource(epRunner).
		Build()

	runningPod := &corev1.Pod{
		Name:      "ss1-runner-busy-running",
		Namespace: "gha-runners",
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	remoteClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(runningPod).
		Build()

	r := &EphemeralRunnerReconciler{
		Client:         fakeClient,
		Scheme:         scheme,
		RemoteProvider: &fakeRemoteProvider{client: remoteClient},
	}

	// Reconcile: Remote pod is actively running, so it should requeue after 5 seconds to let the job finish
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "ss1-runner-busy-running",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("expected RequeueAfter: 5s while pod is running, got %v", res.RequeueAfter)
	}

	// Pod finishes
	runningPod.Status.Phase = corev1.PodSucceeded
	if err := remoteClient.Status().Update(context.Background(), runningPod); err != nil {
		t.Fatalf("failed to update pod status: %v", err)
	}

	// Next Reconcile: Pod succeeded, finalizer should now be removed
	res, err = r.Reconcile(context.Background(), ctrl.Request{
		Namespace: "default", Name: "ss1-runner-busy-running",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("expected RequeueAfter: 0 after pod succeeded, got %v", res.RequeueAfter)
	}

	var updatedRunner ghav1alpha1.EphemeralRunner
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "ss1-runner-busy-running"}, &updatedRunner)
	if err == nil && len(updatedRunner.Finalizers) != 0 {
		t.Errorf("expected finalizer to be removed, got %v", updatedRunner.Finalizers)
	}
}
