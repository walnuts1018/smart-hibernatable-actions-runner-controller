package listener

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/actions/scaleset"
	scalesetlistener "github.com/actions/scaleset/listener"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/githubscaleset"
)

const validRSAKey = `-----BEGIN PRIVATE KEY-----
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

type fakeScaleSetSession struct {
	runCalled    bool
	closeCalled  bool
	maxRunners   int
	runReturnErr error
	runBlockChan chan struct{}
}

func (s *fakeScaleSetSession) Run(ctx context.Context, scaler scalesetlistener.Scaler) error {
	s.runCalled = true
	if s.runBlockChan != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.runBlockChan:
			return s.runReturnErr
		}
	}
	return s.runReturnErr
}

func (s *fakeScaleSetSession) SetMaxRunners(count int) {
	s.maxRunners = count
}

func (s *fakeScaleSetSession) Close(ctx context.Context) error {
	s.closeCalled = true
	return nil
}

type fakeScaleSetClient struct {
	session *fakeScaleSetSession
	err     error
}

func (c *fakeScaleSetClient) GetOrCreateScaleSet(ctx context.Context, scaleSetName, runnerGroup string) (int64, error) {
	return 100, nil
}
func (c *fakeScaleSetClient) GenerateJITConfig(ctx context.Context, scaleSetID int64, runnerName, workFolder string) (*githubscaleset.JITConfigResponse, error) {
	return &githubscaleset.JITConfigResponse{RunnerID: 1, RunnerName: runnerName, EncodedJITConfig: "dummy"}, nil
}
func (c *fakeScaleSetClient) GetRunnerByName(ctx context.Context, runnerName string) (*scaleset.RunnerReference, error) {
	return nil, nil
}
func (c *fakeScaleSetClient) DeleteScaleSet(ctx context.Context, scaleSetID int64) error {
	return nil
}
func (c *fakeScaleSetClient) RemoveRunner(ctx context.Context, runnerID int64) error {
	return nil
}
func (c *fakeScaleSetClient) CreateListenerSession(ctx context.Context, scaleSetID int64, owner string, maxCapacity int, recorder scalesetlistener.MetricsRecorder) (githubscaleset.ListenerSession, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.session == nil {
		c.session = &fakeScaleSetSession{}
	}
	c.session.maxRunners = maxCapacity
	return c.session, nil
}

type fakeFactory struct {
	client *fakeScaleSetClient
	err    error
}

func (f *fakeFactory) NewClient(configURL string, auth *scaleset.GitHubAppAuth) (githubscaleset.ScaleSetClient, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

func TestCalculateBackoff(t *testing.T) {
	base := 1 * time.Second
	maxDelay := 30 * time.Second

	d0 := calculateBackoff(0, base, maxDelay)
	if d0 < 800*time.Millisecond || d0 > 1200*time.Millisecond {
		t.Errorf("expected d0 ~ 1s with jitter, got %v", d0)
	}

	d5 := calculateBackoff(5, base, maxDelay)
	if d5 > maxDelay {
		t.Errorf("expected backoff capped at %v, got %v", maxDelay, d5)
	}
}

func TestWaitWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Context cancelled before wait
	cancel()
	if waitWithContext(ctx, 1*time.Second) {
		t.Errorf("expected waitWithContext to return false on cancelled context")
	}

	// Normal wait
	ctx2 := context.Background()
	start := time.Now()
	if !waitWithContext(ctx2, 10*time.Millisecond) {
		t.Errorf("expected waitWithContext to return true on timeout")
	}
	if time.Since(start) < 10*time.Millisecond {
		t.Errorf("expected wait duration at least 10ms")
	}
}

func TestUpdateListenerReadyStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	ss := &ghav1alpha1.RunnerScaleSet{
		Namespace: "default",
		Name:      "test-ss",
		Status: ghav1alpha1.RunnerScaleSetStatus{
			Listener: ghav1alpha1.ListenerStatus{
				Ready: false,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ss).WithStatusSubresource(ss).Build()

	// Update to ready = true
	updateListenerReadyStatus(context.Background(), fakeClient, "default", "test-ss", true)

	var updated ghav1alpha1.RunnerScaleSet
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &updated); err != nil {
		t.Fatalf("failed to get RunnerScaleSet: %v", err)
	}

	if !updated.Status.Listener.Ready {
		t.Errorf("expected listener ready to be true")
	}
	if updated.Status.Listener.LastConnectedTime == nil {
		t.Errorf("expected LastConnectedTime to be set")
	}

	// Update to ready = false
	updateListenerReadyStatus(context.Background(), fakeClient, "default", "test-ss", false)

	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "test-ss"}, &updated); err != nil {
		t.Fatalf("failed to get RunnerScaleSet: %v", err)
	}

	if updated.Status.Listener.Ready {
		t.Errorf("expected listener ready to be false")
	}

	// Nil client check
	updateListenerReadyStatus(context.Background(), nil, "default", "test-ss", true)

	// Non-existent scaleSet should not panic
	updateListenerReadyStatus(context.Background(), fakeClient, "default", "non-existent", true)
}

func TestSyncStatisticsStatusLoop(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	ss := &ghav1alpha1.RunnerScaleSet{
		Namespace: "default",
		Name:      "test-ss",
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ss).WithStatusSubresource(ss).Build()
	store := githubscaleset.NewStatisticsStore()

	ctx := t.Context()

	// Run loop in background
	go syncStatisticsStatusLoop(ctx, fakeClient, "default", "test-ss", store)

	// Update store with new stats
	store.SetLatest(&scaleset.RunnerScaleSetStatistic{
		TotalAvailableJobs:     5,
		TotalAcquiredJobs:      3,
		TotalAssignedJobs:      2,
		TotalRunningJobs:       1,
		TotalRegisteredRunners: 4,
		TotalBusyRunners:       1,
		TotalIdleRunners:       3,
	})

	// Wait briefly for ticker / patch
	time.Sleep(100 * time.Millisecond)

	// Check nil client or nil store does not panic
	syncStatisticsStatusLoop(ctx, nil, "default", "test-ss", store)
	syncStatisticsStatusLoop(ctx, fakeClient, "default", "test-ss", nil)
}

func TestRunListenerSession_Lifecycle(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	secret := &corev1.Secret{
		Namespace: "default",
		Name:      "gha-secret",
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte(validRSAKey),
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Namespace: "default",
		Name:      "test-ss",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				ConfigURL: "https://github.com/example/org",
				CredentialsSecretRef: corev1.LocalObjectReference{
					Name: "gha-secret",
				},
			},
		},
		Status: ghav1alpha1.RunnerScaleSetStatus{
			ScaleSetID:          100,
			EffectiveMaxRunners: 5,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret, scaleSet).WithStatusSubresource(scaleSet).Build()

	session := &fakeScaleSetSession{
		runBlockChan: make(chan struct{}),
	}
	factory := &fakeFactory{
		client: &fakeScaleSetClient{session: session},
	}

	tracker := NewReadinessTracker()

	opts := RunnerOptions{
		Namespace:       "default",
		Name:            "test-ss",
		K8sClient:       fakeClient,
		ScaleSetFactory: factory,
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		runListenerSession(ctx, opts, tracker, "test-owner")
	}()

	// Wait for session to start
	time.Sleep(100 * time.Millisecond)

	if !tracker.IsReady() {
		t.Errorf("expected tracker to be ready")
	}

	// Cancel context to end session
	cancel()
	time.Sleep(100 * time.Millisecond)

	if !session.closeCalled {
		t.Errorf("expected session.Close to be called on termination")
	}
}

func TestRunListenerSession_MissingScaleSetIDAndRecover(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	secret := &corev1.Secret{
		Namespace: "default",
		Name:      "gha-secret",
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte(validRSAKey),
		},
	}

	// ScaleSet without ScaleSetID
	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Namespace: "default",
		Name:      "test-ss-noid",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				ConfigURL: "https://github.com/example/org",
				CredentialsSecretRef: corev1.LocalObjectReference{
					Name: "gha-secret",
				},
			},
		},
		Status: ghav1alpha1.RunnerScaleSetStatus{
			ScaleSetID: 0,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret, scaleSet).WithStatusSubresource(scaleSet).Build()

	session := &fakeScaleSetSession{}
	factory := &fakeFactory{
		client: &fakeScaleSetClient{session: session},
	}

	tracker := NewReadinessTracker()
	opts := RunnerOptions{
		Namespace:       "default",
		Name:            "test-ss-noid",
		K8sClient:       fakeClient,
		ScaleSetFactory: factory,
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel quickly so it does not loop forever
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	runListenerSession(ctx, opts, tracker, "test-owner")
}

func TestRunListenerSession_FactoryError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ghav1alpha1.AddToScheme(scheme)

	secret := &corev1.Secret{
		Namespace: "default",
		Name:      "gha-secret",
		Data: map[string][]byte{
			"github_app_id":              []byte("12345"),
			"github_app_installation_id": []byte("67890"),
			"github_app_private_key":     []byte(validRSAKey),
		},
	}

	scaleSet := &ghav1alpha1.RunnerScaleSet{
		Namespace: "default",
		Name:      "test-ss-error",
		Spec: ghav1alpha1.RunnerScaleSetSpec{
			GitHub: ghav1alpha1.GitHubScaleSetSpec{
				ConfigURL: "https://github.com/example/org",
				CredentialsSecretRef: corev1.LocalObjectReference{
					Name: "gha-secret",
				},
			},
		},
		Status: ghav1alpha1.RunnerScaleSetStatus{
			ScaleSetID: 100,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret, scaleSet).WithStatusSubresource(scaleSet).Build()

	factory := &fakeFactory{
		err: errors.New("cannot create client"),
	}

	tracker := NewReadinessTracker()
	opts := RunnerOptions{
		Namespace:       "default",
		Name:            "test-ss-error",
		K8sClient:       fakeClient,
		ScaleSetFactory: factory,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	runListenerSession(ctx, opts, tracker, "test-owner")
}
