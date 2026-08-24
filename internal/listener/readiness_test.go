package listener

import (
	"testing"
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
}
