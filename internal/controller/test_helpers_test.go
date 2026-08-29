package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/githubscaleset"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/redfish"
)

func setupFakeClientBuilder(scheme *runtime.Scheme) *fake.ClientBuilder {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&ghav1alpha1.EphemeralRunner{}, IndexScaleSetRefName, func(obj client.Object) []string {
			er, ok := obj.(*ghav1alpha1.EphemeralRunner)
			if !ok || er.Spec.ScaleSetRef.Name == "" {
				return nil
			}
			return []string{er.Spec.ScaleSetRef.Name}
		}).
		WithIndex(&ghav1alpha1.EphemeralRunnerSet{}, IndexScaleSetRefName, func(obj client.Object) []string {
			ers, ok := obj.(*ghav1alpha1.EphemeralRunnerSet)
			if !ok || ers.Spec.ScaleSetRef.Name == "" {
				return nil
			}
			return []string{ers.Spec.ScaleSetRef.Name}
		}).
		WithIndex(&ghav1alpha1.RunnerScaleSet{}, IndexNodePoolRefName, func(obj client.Object) []string {
			ss, ok := obj.(*ghav1alpha1.RunnerScaleSet)
			if !ok || ss.Spec.NodePoolRef.Name == "" {
				return nil
			}
			return []string{ss.Spec.NodePoolRef.Name}
		}).
		WithIndex(&ghav1alpha1.RunnerMachine{}, IndexMachineNodePoolRefName, func(obj client.Object) []string {
			rm, ok := obj.(*ghav1alpha1.RunnerMachine)
			if !ok || rm.Spec.NodePoolRef == nil || rm.Spec.NodePoolRef.Name == "" {
				return nil
			}
			return []string{rm.Spec.NodePoolRef.Name}
		}).
		WithIndex(&ghav1alpha1.RunnerMachine{}, IndexClusterRefName, func(obj client.Object) []string {
			rm, ok := obj.(*ghav1alpha1.RunnerMachine)
			if !ok || rm.Spec.ClusterRef.Name == "" {
				return nil
			}
			return []string{rm.Spec.ClusterRef.Name}
		})
}

type fakeRemoteProvider struct {
	client     client.Client
	healthErr  error
	node       *corev1.Node
	nodeErr    error
	clusterUID string
	err        error
}

func (f *fakeRemoteProvider) GetClient(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) (client.Client, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.client, nil
}

func (f *fakeRemoteProvider) CheckHealth(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) error {
	if f.healthErr != nil {
		return f.healthErr
	}
	return f.err
}

func (f *fakeRemoteProvider) GetNode(ctx context.Context, cluster *ghav1alpha1.RunnerCluster, nodeName string) (*corev1.Node, error) {
	if f.nodeErr != nil {
		return nil, f.nodeErr
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.node, nil
}

func (f *fakeRemoteProvider) GetClusterUID(ctx context.Context, cluster *ghav1alpha1.RunnerCluster) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.clusterUID != "" {
		return f.clusterUID, nil
	}
	return "fake-cluster-uid", nil
}

func (f *fakeRemoteProvider) InvalidateCache(key client.ObjectKey) {}

type fakePowerControllerFactory struct {
	fakeCtrl *fakePowerController
}

func (f *fakePowerControllerFactory) NewController(spec ghav1alpha1.RedfishSpec, username, password string, caCert []byte) (redfish.PowerController, error) {
	return f.fakeCtrl, nil
}

type fakePowerController struct {
	powerState     ghav1alpha1.PowerState
	powerOnCalled  bool
	shutdownCalled bool
	shutdownErr    error
	forceOffCalled bool
}

func (f *fakePowerController) GetPowerState(ctx context.Context) (ghav1alpha1.PowerState, error) {
	return f.powerState, nil
}

func (f *fakePowerController) PowerOn(ctx context.Context) error {
	f.powerOnCalled = true
	f.powerState = ghav1alpha1.PowerStateOn
	return nil
}

func (f *fakePowerController) GracefulShutdown(ctx context.Context) error {
	f.shutdownCalled = true
	if f.shutdownErr != nil {
		return f.shutdownErr
	}
	f.powerState = ghav1alpha1.PowerStatePoweringOff
	return nil
}

func (f *fakePowerController) ForceOff(ctx context.Context) error {
	f.forceOffCalled = true
	f.powerState = ghav1alpha1.PowerStateOff
	return nil
}

func (f *fakePowerController) ValidateSupport(ctx context.Context) error {
	return nil
}

type fakeScaleSetFactory struct {
	fakeClient *fakeScaleSetClient
	err        error
}

func (f *fakeScaleSetFactory) NewClient(configURL string, auth *scaleset.GitHubAppAuth) (githubscaleset.ScaleSetClient, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.fakeClient, nil
}

type fakeScaleSetClient struct {
	scaleSetID         int64
	scaleSetName       string
	runnerGroup        string
	getOrCreateErr     error
	deleteErr          error
	generateJITErr     error
	removeRunnerErr    error
	encodedJITConfig   string
	existingRunnerRef  *scaleset.RunnerReference
	createdListener    *listener.Listener
	createListenerErr  error
	deletedScaleSetIDs []int64
	removedRunnerIDs   []int64
}

func (c *fakeScaleSetClient) GetOrCreateScaleSet(ctx context.Context, scaleSetName, runnerGroup string) (int64, error) {
	if c.getOrCreateErr != nil {
		return 0, c.getOrCreateErr
	}
	c.scaleSetName = scaleSetName
	c.runnerGroup = runnerGroup
	if c.scaleSetID == 0 {
		c.scaleSetID = 100
	}
	return c.scaleSetID, nil
}

func (c *fakeScaleSetClient) GenerateJITConfig(ctx context.Context, scaleSetID int64, runnerName, workFolder string) (*githubscaleset.JITConfigResponse, error) {
	if c.generateJITErr != nil {
		return nil, c.generateJITErr
	}
	enc := "fake-encoded-jit-config"
	if c.encodedJITConfig != "" {
		enc = c.encodedJITConfig
	}
	return &githubscaleset.JITConfigResponse{
		RunnerID:         200,
		RunnerName:       runnerName,
		EncodedJITConfig: enc,
	}, nil
}

func (c *fakeScaleSetClient) GetRunnerByName(ctx context.Context, runnerName string) (*scaleset.RunnerReference, error) {
	return c.existingRunnerRef, nil
}

func (c *fakeScaleSetClient) DeleteScaleSet(ctx context.Context, scaleSetID int64) error {
	if c.deleteErr != nil {
		return c.deleteErr
	}
	c.deletedScaleSetIDs = append(c.deletedScaleSetIDs, scaleSetID)
	return nil
}

func (c *fakeScaleSetClient) RemoveRunner(ctx context.Context, runnerID int64) error {
	if c.removeRunnerErr != nil {
		return c.removeRunnerErr
	}
	c.removedRunnerIDs = append(c.removedRunnerIDs, runnerID)
	return nil
}

type fakeListenerSession struct {
	listener *listener.Listener
	closed   bool
}

func (s *fakeListenerSession) Run(ctx context.Context, scaler listener.Scaler) error {
	if s.listener != nil {
		return s.listener.Run(ctx, scaler)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s *fakeListenerSession) SetMaxRunners(count int) {
	if s.listener != nil {
		s.listener.SetMaxRunners(count)
	}
}

func (s *fakeListenerSession) Close(ctx context.Context) error {
	s.closed = true
	return nil
}

func (c *fakeScaleSetClient) CreateListenerSession(ctx context.Context, scaleSetID int64, owner string, maxCapacity int, recorder listener.MetricsRecorder) (githubscaleset.ListenerSession, error) {
	if c.createListenerErr != nil {
		return nil, c.createListenerErr
	}
	return &fakeListenerSession{
		listener: c.createdListener,
	}, nil
}
