package githubscaleset

import (
	"context"
	"fmt"
	"time"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"
)

// ScaleSetStatistics represents runner scale set statistics from GitHub Actions.
type ScaleSetStatistics struct {
	TotalAssignedJobs      int
	TotalRunningJobs       int
	TotalRegisteredRunners int
	TotalBusyRunners       int
	TotalIdleRunners       int
	UpdatedAt              time.Time
}

// JITConfigResponse holds JIT configuration and assigned GitHub Runner reference.
type JITConfigResponse struct {
	RunnerID         int64
	RunnerName       string
	EncodedJITConfig string
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
	CreateListenerSession(ctx context.Context, scaleSetID int64, maxCapacity int, scaler listener.Scaler, recorder listener.MetricsRecorder) (*ListenerSession, error)

	// CreateListener creates a new listener instance for this scale set (for backward compatibility).
	CreateListener(ctx context.Context, scaleSetID int64, maxCapacity int, scaler listener.Scaler, recorder listener.MetricsRecorder) (*listener.Listener, error)
}

// ListenerSession pairs a Listener with its underlying MessageSessionClient for graceful shutdown.
type ListenerSession struct {
	Listener      *listener.Listener
	SessionClient *scaleset.MessageSessionClient
}

// Close gracefully closes the GitHub Actions message session.
func (s *ListenerSession) Close(ctx context.Context) error {
	if s.SessionClient != nil {
		return s.SessionClient.Close(ctx)
	}
	return nil
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
	configURL string
}

// NewClient creates a new ScaleSetClient.
func NewClient(configURL string, auth *scaleset.GitHubAppAuth) (ScaleSetClient, error) {
	rawClient, err := scaleset.NewClientWithGitHubApp(scaleset.ClientWithGitHubAppConfig{
		GitHubConfigURL: configURL,
		GitHubAppAuth:   *auth,
		SystemInfo: scaleset.SystemInfo{
			Version: "gha-baremetal-operator/v1alpha1",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create scaleset client: %w", err)
	}

	return &clientImpl{
		rawClient: rawClient,
		configURL: configURL,
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
	if err == nil && existing != nil {
		return int64(existing.ID), nil
	}

	// Create new scale set
	created, err := c.rawClient.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
		Name:          scaleSetName,
		RunnerGroupID: rg.ID,
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

	var runnerID int64
	if res.Runner != nil {
		runnerID = int64(res.Runner.ID)
	}

	return &JITConfigResponse{
		RunnerID:         runnerID,
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

func (c *clientImpl) CreateListenerSession(ctx context.Context, scaleSetID int64, maxCapacity int, _ listener.Scaler, recorder listener.MetricsRecorder) (*ListenerSession, error) {
	opts := []listener.Option{}
	if recorder != nil {
		opts = append(opts, listener.WithMetricsRecorder(recorder))
	}

	sessionClient, err := c.rawClient.MessageSessionClient(ctx, int(scaleSetID), "gha-listener")
	if err != nil {
		return nil, fmt.Errorf("failed to create message session client: %w", err)
	}

	l, err := listener.New(sessionClient, listener.Config{
		ScaleSetID: int(scaleSetID),
		MaxRunners: maxCapacity,
	}, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	return &ListenerSession{
		Listener:      l,
		SessionClient: sessionClient,
	}, nil
}

func (c *clientImpl) CreateListener(ctx context.Context, scaleSetID int64, maxCapacity int, scaler listener.Scaler, recorder listener.MetricsRecorder) (*listener.Listener, error) {
	sess, err := c.CreateListenerSession(ctx, scaleSetID, maxCapacity, scaler, recorder)
	if err != nil {
		return nil, err
	}
	return sess.Listener, nil
}
