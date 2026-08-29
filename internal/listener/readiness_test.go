package listener

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestReadinessTracker(t *testing.T) {
	tracker := NewReadinessTracker()

	// Initialized process is ready for Kubernetes readiness probe
	if !tracker.IsReady() {
		t.Fatal("expected tracker to be ready (initialized) for Kubernetes probe")
	}

	// But not leader yet
	if tracker.IsLeader() {
		t.Fatal("expected tracker to not be leader initially")
	}

	tracker.SetLeaseAcquired(true)
	if tracker.IsLeader() {
		t.Fatal("expected tracker to not be leader with only lease")
	}

	tracker.SetGitHubAuthenticated(true)
	if tracker.IsLeader() {
		t.Fatal("expected tracker to not be leader with lease and auth")
	}

	tracker.SetSessionEstablished(true)
	if tracker.IsLeader() {
		t.Fatal("expected tracker to not be leader without initial statistics")
	}

	tracker.SetInitialStatisticsReceived(true)
	if !tracker.IsLeader() {
		t.Fatal("expected tracker to be leader when all conditions are true")
	}

	tracker.Reset()
	if tracker.IsLeader() {
		t.Fatal("expected tracker to not be leader after reset")
	}
	if !tracker.leaseAcquired {
		t.Fatal("expected leaseAcquired to remain true after reset")
	}

	tracker.SetLeaseAcquired(false)
	if tracker.leaseAcquired {
		t.Fatal("expected leaseAcquired to be false")
	}

	tracker.SetInitialized(false)
	if tracker.IsReady() {
		t.Fatal("expected tracker to not be ready when initialized is false")
	}
}

func TestStartHTTPServer_Probes(t *testing.T) {
	tracker := NewReadinessTracker()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	addr := "127.0.0.1:18089"
	err := StartHTTPServer(ctx, addr, tracker)
	if err != nil {
		t.Fatalf("failed to start http server: %v", err)
	}

	// Give server a moment to start
	time.Sleep(50 * time.Millisecond)

	client := &http.Client{}

	// 1. /healthz -> 200
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to get /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /healthz, got %d", resp.StatusCode)
	}

	// 2. /readyz when initialized -> 200
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/readyz", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("failed to get /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for /readyz when ready, got %d", resp.StatusCode)
	}

	// 3. /readyz when not initialized -> 503
	tracker.SetInitialized(false)
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/readyz", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("failed to get /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for /readyz when not ready, got %d", resp.StatusCode)
	}

	// 4. /leaderz when not leader -> 503
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/leaderz", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("failed to get /leaderz: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || string(body) != "standby" {
		t.Errorf("expected 503 standby for /leaderz, got %d / %s", resp.StatusCode, string(body))
	}

	// 5. /leaderz when leader -> 200
	tracker.SetLeaseAcquired(true)
	tracker.SetGitHubAuthenticated(true)
	tracker.SetSessionEstablished(true)
	tracker.SetInitialStatisticsReceived(true)

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/leaderz", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("failed to get /leaderz: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "leader" {
		t.Errorf("expected 200 leader for /leaderz, got %d / %s", resp.StatusCode, string(body))
	}
}
