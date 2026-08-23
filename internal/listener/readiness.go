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

// ReadinessTrackerはListenerの準備完了状態を追跡します。
type ReadinessTracker struct {
	mu                        sync.RWMutex
	leaseAcquired             bool
	githubAuthenticated       bool
	sessionEstablished        bool
	initialStatisticsReceived bool
}

// NewReadinessTrackerは新しいReadinessTrackerを初期化します。
func NewReadinessTracker() *ReadinessTracker {
	return &ReadinessTracker{}
}

// SetLeaseAcquiredはLease取得状態を更新します。
func (r *ReadinessTracker) SetLeaseAcquired(val bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaseAcquired = val
}

// SetGitHubAuthenticatedはGitHub認証状態を更新します。
func (r *ReadinessTracker) SetGitHubAuthenticated(val bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.githubAuthenticated = val
}

// SetSessionEstablishedはGitHubセッション確立状態を更新します。
func (r *ReadinessTracker) SetSessionEstablished(val bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionEstablished = val
}

// SetInitialStatisticsReceivedは初回統計情報の受信状態を更新します。
func (r *ReadinessTracker) SetInitialStatisticsReceived(val bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initialStatisticsReceived = val
}

// Resetはセッション切断時などに状態をリセットします。
func (r *ReadinessTracker) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.githubAuthenticated = false
	r.sessionEstablished = false
	r.initialStatisticsReceived = false
}

// IsReadyはすべてのReadiness条件が満たされているかを判定します。
func (r *ReadinessTracker) IsReady() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.leaseAcquired && r.githubAuthenticated && r.sessionEstablished && r.initialStatisticsReceived
}

// StartHTTPServerはProbeおよびMetrics用のHTTPサーバーを起動します。
func StartHTTPServer(ctx context.Context, probeAddr, metricsAddr string, tracker *ReadinessTracker) error {
	mux := http.NewServeMux()

	// Liveness probe:プロセスが生存していれば200OK
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Readiness probe:全条件が揃っている場合のみ200OK
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if tracker.IsReady() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = probeServer.Shutdown(shutdownCtx)
		if metricsServer != nil {
			_ = metricsServer.Shutdown(shutdownCtx)
		}
	}()

	return nil
}
