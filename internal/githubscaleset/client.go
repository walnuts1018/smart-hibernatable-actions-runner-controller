package githubscaleset

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
)

// JITConfigResponse holds JIT configuration and assigned GitHub Runner reference.
type JITConfigResponse struct {
	RunnerID         int64
	RunnerName       string
	EncodedJITConfig string
}

// ListenerSession manages the lifecycle of a listener and its underlying message session.
type ListenerSession interface {
	Run(ctx context.Context, scaler listener.Scaler) error
	SetMaxRunners(count int)
	Close(ctx context.Context) error
}

type listenerSessionImpl struct {
	listener      *listener.Listener
	sessionClient *scaleset.MessageSessionClient
}

func (s *listenerSessionImpl) Run(ctx context.Context, scaler listener.Scaler) error {
	return s.listener.Run(ctx, scaler)
}

func (s *listenerSessionImpl) SetMaxRunners(count int) {
	s.listener.SetMaxRunners(count)
}

func (s *listenerSessionImpl) Close(ctx context.Context) error {
	if s.sessionClient != nil {
		return s.sessionClient.Close(ctx)
	}
	return nil
}

// ScaleSetClient defines operations on GitHub Actions Scale Sets.
type ScaleSetClient interface {
	// GetOrCreateScaleSet ensures the RunnerScaleSet exists in GitHub and returns its ID.
	GetOrCreateScaleSet(ctx context.Context, scaleSetName, runnerGroup string) (int64, error)

	// GenerateJITConfig creates a JIT configuration for an ephemeral runner.
	GenerateJITConfig(ctx context.Context, scaleSetID int64, runnerName, workFolder string) (*JITConfigResponse, error)

	// GetRunnerByName retrieves an existing runner reference by name if registered in GitHub Actions.
	GetRunnerByName(ctx context.Context, runnerName string) (*scaleset.RunnerReference, error)

	// DeleteScaleSet removes the scale set from GitHub Actions.
	DeleteScaleSet(ctx context.Context, scaleSetID int64) error

	// RemoveRunner removes an individual runner from the scale set in GitHub Actions.
	RemoveRunner(ctx context.Context, runnerID int64) error

	// CreateListenerSession creates a new listener session (including MessageSessionClient) for this scale set.
	CreateListenerSession(ctx context.Context, scaleSetID int64, owner string, maxCapacity int, recorder listener.MetricsRecorder) (ListenerSession, error)
}

// ScaleSetClientFactory creates ScaleSetClient instances.
type ScaleSetClientFactory interface {
	NewClient(configURL string, auth *scaleset.GitHubAppAuth) (ScaleSetClient, error)
}

type defaultClientFactory struct{}

// NewScaleSetClientFactory creates a new ScaleSetClientFactory.
func NewScaleSetClientFactory() ScaleSetClientFactory {
	return &defaultClientFactory{}
}

func (f *defaultClientFactory) NewClient(configURL string, auth *scaleset.GitHubAppAuth) (ScaleSetClient, error) {
	return NewClient(configURL, auth)
}

type clientImpl struct {
	rawClient *scaleset.Client
}

// NewClient creates a new ScaleSetClient.
func NewClient(configURL string, auth *scaleset.GitHubAppAuth) (ScaleSetClient, error) {
	if auth == nil {
		return nil, fmt.Errorf("GitHub App auth is required")
	}

	rawClient, err := scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
		GitHubConfigURL: configURL,
		GitHubAppAuth:   *auth,
		SystemInfo: scaleset.SystemInfo{
			System:    "smart-hibernatable-actions-runner-controller",
			Version:   "v1alpha1",
			Subsystem: "controller",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create scaleset client: %w", err)
	}

	return &clientImpl{
		rawClient: rawClient,
	}, nil
}

func (c *clientImpl) GetOrCreateScaleSet(ctx context.Context, scaleSetName, runnerGroup string) (int64, error) {
	if runnerGroup == "" {
		runnerGroup = scaleset.DefaultRunnerGroup
	}

	rg, err := c.rawClient.GetRunnerGroupByName(ctx, runnerGroup)
	if err != nil {
		return 0, fmt.Errorf("failed to get runner group %q: %w", runnerGroup, err)
	}

	existing, err := c.rawClient.GetRunnerScaleSet(ctx, rg.ID, scaleSetName)
	if err != nil {
		return 0, fmt.Errorf("failed to get runner scale set %q: %w", scaleSetName, err)
	}
	if existing != nil {
		return int64(existing.ID), nil
	}

	// Create new scale set
	created, err := c.rawClient.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
		Name:          scaleSetName,
		RunnerGroupID: rg.ID,
		RunnerSetting: scaleset.RunnerSetting{
			DisableUpdate: true,
		},
	})
	if err != nil {
		return 0, fmt.Errorf("failed to create runner scale set %q: %w", scaleSetName, err)
	}

	return int64(created.ID), nil
}

func (c *clientImpl) GenerateJITConfig(ctx context.Context, scaleSetID int64, runnerName, workFolder string) (*JITConfigResponse, error) {
	if workFolder == "" {
		workFolder = "_work"
	}

	setting := &scaleset.RunnerScaleSetJitRunnerSetting{
		Name:       runnerName,
		WorkFolder: workFolder,
	}

	res, err := c.rawClient.GenerateJitRunnerConfig(ctx, setting, int(scaleSetID))
	if err != nil {
		return nil, fmt.Errorf("failed to generate JIT config for runner %q: %w", runnerName, err)
	}

	if res.Runner == nil {
		return nil, fmt.Errorf("GitHub returned JIT config without runner reference for %q", runnerName)
	}

	if res.EncodedJITConfig == "" {
		return nil, fmt.Errorf("GitHub returned empty JIT config for runner %q", runnerName)
	}

	return &JITConfigResponse{
		RunnerID:         int64(res.Runner.ID),
		RunnerName:       runnerName,
		EncodedJITConfig: res.EncodedJITConfig,
	}, nil
}

func (c *clientImpl) GetRunnerByName(ctx context.Context, runnerName string) (*scaleset.RunnerReference, error) {
	return c.rawClient.GetRunnerByName(ctx, runnerName)
}

func (c *clientImpl) DeleteScaleSet(ctx context.Context, scaleSetID int64) error {
	err := c.rawClient.DeleteRunnerScaleSet(ctx, int(scaleSetID))
	if err != nil {
		return fmt.Errorf("failed to delete runner scale set %d: %w", scaleSetID, err)
	}
	return nil
}

func (c *clientImpl) RemoveRunner(ctx context.Context, runnerID int64) error {
	err := c.rawClient.RemoveRunner(ctx, runnerID)
	if err != nil {
		return fmt.Errorf("failed to remove runner %d: %w", runnerID, err)
	}
	return nil
}

func (c *clientImpl) CreateListenerSession(ctx context.Context, scaleSetID int64, owner string, maxCapacity int, recorder listener.MetricsRecorder) (ListenerSession, error) {
	if owner == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			owner = h
		} else {
			owner = fmt.Sprintf("listener-%d", time.Now().UnixNano())
		}
	}

	sessionClient, err := c.rawClient.MessageSessionClient(ctx, int(scaleSetID), owner)
	if err != nil {
		return nil, fmt.Errorf("failed to create message session client: %w", err)
	}

	opts := []listener.Option{}
	if recorder != nil {
		opts = append(opts, listener.WithMetricsRecorder(recorder))
	}

	l, err := listener.New(sessionClient, listener.Config{
		ScaleSetID: int(scaleSetID),
		MaxRunners: maxCapacity,
	}, opts...)
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = sessionClient.Close(closeCtx)
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	return &listenerSessionImpl{
		listener:      l,
		sessionClient: sessionClient,
	}, nil
}
