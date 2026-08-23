package listener

import (
	"testing"
)

func TestReadinessTracker(t *testing.T) {
	tracker := NewReadinessTracker()

	if tracker.IsReady() {
		t.Fatal("expected tracker to not be ready initially")
	}

	tracker.SetLeaseAcquired(true)
	if tracker.IsReady() {
		t.Fatal("expected tracker to not be ready with only lease")
	}

	tracker.SetGitHubAuthenticated(true)
	if tracker.IsReady() {
		t.Fatal("expected tracker to not be ready with lease and auth")
	}

	tracker.SetSessionEstablished(true)
	if tracker.IsReady() {
		t.Fatal("expected tracker to not be ready without initial statistics")
	}

	tracker.SetInitialStatisticsReceived(true)
	if !tracker.IsReady() {
		t.Fatal("expected tracker to be ready when all conditions are true")
	}

	tracker.Reset()
	if tracker.IsReady() {
		t.Fatal("expected tracker to not be ready after reset")
	}
	if !tracker.leaseAcquired {
		t.Fatal("expected leaseAcquired to remain true after reset")
	}

	tracker.SetLeaseAcquired(false)
	if tracker.leaseAcquired {
		t.Fatal("expected leaseAcquired to be false")
	}
}
