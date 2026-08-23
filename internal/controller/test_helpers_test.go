package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/githubscaleset"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/redfish"
)

type fakeRemoteProvider struct {
	client    client.Client
	healthErr error
	node      *corev1.Node
	nodeErr   error
	err       error
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

func (f *fakeRemoteProvider) InvalidateCache(clusterKey string) {}

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
	scaleSetID        int64
	scaleSetName       string
	runnerGroup        string
	getOrCreateErr     error
	deleteErr          error
	generateJITErr     error
	removeRunnerErr    error
	encodedJITConfig   string
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

func (c *fakeScaleSetClient) GenerateJITConfig(ctx context.Context, scaleSetID int64, runnerName, workFolder string) (string, error) {
	if c.generateJITErr != nil {
		return "", c.generateJITErr
	}
	if c.encodedJITConfig != "" {
		return c.encodedJITConfig, nil
	}
	return "fake-encoded-jit-config", nil
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

func (c *fakeScaleSetClient) CreateListener(ctx context.Context, scaleSetID int64, maxCapacity int, scaler listener.Scaler, recorder listener.MetricsRecorder) (*listener.Listener, error) {
	if c.createListenerErr != nil {
		return nil, c.createListenerErr
	}
	return c.createdListener, nil
}
