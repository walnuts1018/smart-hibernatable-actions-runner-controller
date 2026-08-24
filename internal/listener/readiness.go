package listener

import (
	"context"
	"net/http"
	"sync"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

var readinessLogger = ctrl.Log.WithName("listener-readiness")

// ReadinessTracker tracks the ready and leadership state of the listener.
type ReadinessTracker struct {
	mu                        sync.RWMutex
	initialized               bool
	leaseAcquired             bool
	githubAuthenticated       bool
	sessionEstablished        bool
	initialStatisticsReceived bool
}

// NewReadinessTracker initializes a new ReadinessTracker.
func NewReadinessTracker() *ReadinessTracker {
	return &ReadinessTracker{
		initialized: true,
	}
}

// SetInitialized updates the initialized state.
func (r *ReadinessTracker) SetInitialized(val bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initialized = val
}

// SetLeaseAcquired updates the lease acquired state.
func (r *ReadinessTracker) SetLeaseAcquired(val bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaseAcquired = val
}

// SetGitHubAuthenticated updates the GitHub authenticated state.
func (r *ReadinessTracker) SetGitHubAuthenticated(val bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.githubAuthenticated = val
}

// SetSessionEstablished updates the GitHub session established state.
func (r *ReadinessTracker) SetSessionEstablished(val bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionEstablished = val
}

// SetInitialStatisticsReceived updates the initial statistics received state.
func (r *ReadinessTracker) SetInitialStatisticsReceived(val bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initialStatisticsReceived = val
}

// Reset resets the tracking state on disconnection.
func (r *ReadinessTracker) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.githubAuthenticated = false
	r.sessionEstablished = false
	r.initialStatisticsReceived = false
}

// IsReady returns true if the listener process is initialized and ready to participate in lease election.
// Standby pods return true so they count towards available replicas and can be scraped by PodMonitor.
func (r *ReadinessTracker) IsReady() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.initialized
}

// IsLeader returns true if this listener holds the leader lease and has an active GitHub session.
func (r *ReadinessTracker) IsLeader() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.leaseAcquired && r.githubAuthenticated && r.sessionEstablished && r.initialStatisticsReceived
}

// StartHTTPServer starts the HTTP server for probes.
func StartHTTPServer(ctx context.Context, probeAddr string, tracker *ReadinessTracker) error {
	mux := http.NewServeMux()

	// Liveness probe: プロセスが生存していれば 200 OK
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			readinessLogger.Error(err, "failed to write healthz response")
		}
	})

	// Readiness probe: プロセスが正常初期化されていれば 200 OK (Standby も 200 OK)
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if tracker.IsReady() {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("ready")); err != nil {
				readinessLogger.Error(err, "failed to write readyz response")
			}
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte("not ready")); err != nil {
				readinessLogger.Error(err, "failed to write readyz response")
			}
		}
	})

	// Leader probe: Active Leader で GitHub Session を保持している場合のみ 200 OK
	mux.HandleFunc("/leaderz", func(w http.ResponseWriter, _ *http.Request) {
		if tracker.IsLeader() {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("leader")); err != nil {
				readinessLogger.Error(err, "failed to write leaderz response")
			}
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte("standby")); err != nil {
				readinessLogger.Error(err, "failed to write leaderz response")
			}
		}
	})

	probeServer := &http.Server{}
	probeServer.Addr = probeAddr
	probeServer.Handler = mux
	probeServer.ReadHeaderTimeout = 5 * time.Second

	go func() {
		readinessLogger.Info("starting probe server", "addr", probeAddr)
		if err := probeServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			readinessLogger.Error(err, "probe server failed")
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := probeServer.Shutdown(shutdownCtx); err != nil {
			readinessLogger.Error(err, "failed to shutdown probe server")
		}
	}()

	return nil
}
