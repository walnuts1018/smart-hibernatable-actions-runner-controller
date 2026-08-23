package listener

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	ctrl "sigs.k8s.io/controller-runtime"
)

var readinessLogger = ctrl.Log.WithName("listener-readiness")

// ReadinessTracker tracks the ready state of the listener.
type ReadinessTracker struct {
	mu                        sync.RWMutex
	leaseAcquired             bool
	githubAuthenticated       bool
	sessionEstablished        bool
	initialStatisticsReceived bool
}

// NewReadinessTracker initializes a new ReadinessTracker.
func NewReadinessTracker() *ReadinessTracker {
	return &ReadinessTracker{}
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

// IsReady returns true if all readiness conditions are satisfied.
func (r *ReadinessTracker) IsReady() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.leaseAcquired && r.githubAuthenticated && r.sessionEstablished && r.initialStatisticsReceived
}

// StartHTTPServer starts the HTTP server for probes and metrics.
func StartHTTPServer(ctx context.Context, probeAddr, metricsAddr string, tracker *ReadinessTracker) error {
	mux := http.NewServeMux()

	// Liveness probe:プロセスが生存していれば200OK
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Readiness probe:全条件が揃っている場合のみ200OK
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if tracker.IsReady() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ready"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("not ready"))
		}
	})

	// Metrics endpoint (同一ポートまたは別ポートで提供可能)
	if probeAddr == metricsAddr || metricsAddr == "" {
		mux.Handle("/metrics", promhttp.Handler())
	}

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

	var metricsServer *http.Server
	if metricsAddr != "" && metricsAddr != probeAddr {
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", promhttp.Handler())
		metricsServer = &http.Server{}
		metricsServer.Addr = metricsAddr
		metricsServer.Handler = metricsMux
		metricsServer.ReadHeaderTimeout = 5 * time.Second
		go func() {
			readinessLogger.Info("starting metrics server", "addr", metricsAddr)
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				readinessLogger.Error(err, "metrics server failed")
			}
		}()
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		probeServer.Shutdown(shutdownCtx)
		if metricsServer != nil {
			metricsServer.Shutdown(shutdownCtx)
		}
	}()

	return nil
}
