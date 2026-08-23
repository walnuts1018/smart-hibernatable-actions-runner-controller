package listener

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/githubscaleset"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
)

var runnerLogger = ctrl.Log.WithName("listener-runner")

func calculateBackoff(attempt int, base time.Duration, maxDelay time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	multiplier := 1 << attempt
	if multiplier <= 0 || multiplier > 1024 {
		multiplier = 1024
	}
	temp := base * time.Duration(multiplier)
	if temp > maxDelay || temp <= 0 {
		temp = maxDelay
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(temp)))
	if err != nil || n == nil {
		return temp / 2
	}
	return time.Duration(n.Int64())
}

// RunnerOptions holds execution options for the listener process.
type RunnerOptions struct {
	Namespace       string
	Name            string
	ProbeAddr       string
	MetricsAddr     string
	Config          *rest.Config
	K8sClient       client.Client
	Clientset       *kubernetes.Clientset
	ScaleSetFactory githubscaleset.ScaleSetClientFactory
}

// RunListenerWithLease performs leader election using Lease and starts the listener session.
func RunListenerWithLease(ctx context.Context, opts RunnerOptions) error {
	tracker := NewReadinessTracker()

	// ProbeおよびMetrics用HTTPサーバーを起動
	if err := StartHTTPServer(ctx, opts.ProbeAddr, opts.MetricsAddr, tracker); err != nil {
		return fmt.Errorf("failed to start http server: %w", err)
	}

	var scaleSet ghav1alpha1.RunnerScaleSet
	if err := opts.K8sClient.Get(ctx, client.ObjectKey{Namespace: opts.Namespace, Name: opts.Name}, &scaleSet); err != nil {
		return fmt.Errorf("failed to fetch RunnerScaleSet: %w", err)
	}

	lockName := fmt.Sprintf("gha-listener-%s", scaleSet.UID)
	id, err := os.Hostname()
	if err != nil {
		id = fmt.Sprintf("listener-%d", time.Now().UnixNano())
	}

	lock := &resourcelock.LeaseLock{
		Client: opts.Clientset.CoordinationV1(),
	}
	lock.LeaseMeta.Name = lockName
	lock.LeaseMeta.Namespace = opts.Namespace
	lock.LockConfig.Identity = id

	callbacks := leaderelection.LeaderCallbacks{
		OnStartedLeading: func(leaderCtx context.Context) {
			runnerLogger.Info("acquired lease, starting scale set listener", "scaleSet", fmt.Sprintf("%s/%s", opts.Namespace, opts.Name))
			tracker.SetLeaseAcquired(true)
			runListenerSession(leaderCtx, opts, tracker)
		},
		OnStoppedLeading: func() {
			runnerLogger.Info("lost lease, stopping listener")
			tracker.SetLeaseAcquired(false)
			tracker.Reset()
		},
	}

	leCfg := leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks:       callbacks,
	}
	leaderelection.RunOrDie(ctx, leCfg)

	return nil
}

func waitWithContext(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func runListenerSession(ctx context.Context, opts RunnerOptions, tracker *ReadinessTracker) {
	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		tracker.Reset()

		var scaleSet ghav1alpha1.RunnerScaleSet
		if err := opts.K8sClient.Get(ctx, client.ObjectKey{Namespace: opts.Namespace, Name: opts.Name}, &scaleSet); err != nil {
			backoff := calculateBackoff(attempt, 2*time.Second, 30*time.Second)
			attempt++
			runnerLogger.Error(err, "failed to get RunnerScaleSet, retrying with backoff", "backoff", backoff)
			if !waitWithContext(ctx, backoff) {
				return
			}
			continue
		}

		if scaleSet.Status.ScaleSetID == 0 {
			runnerLogger.Info("waiting for scaleSetID to be populated by manager controller...")
			if !waitWithContext(ctx, 5*time.Second) {
				return
			}
			continue
		}

		var secret corev1.Secret
		if err := opts.K8sClient.Get(ctx, client.ObjectKey{Namespace: opts.Namespace, Name: scaleSet.Spec.GitHub.CredentialsSecretRef.Name}, &secret); err != nil {
			backoff := calculateBackoff(attempt, 5*time.Second, 1*time.Minute)
			attempt++
			runnerLogger.Error(err, "failed to get GitHub credentials secret, retrying with backoff", "backoff", backoff)
			if !waitWithContext(ctx, backoff) {
				return
			}
			continue
		}

		auth, err := githubscaleset.ParseGitHubAppAuth(secret.Data)
		if err != nil {
			backoff := calculateBackoff(attempt, 10*time.Second, 2*time.Minute)
			attempt++
			runnerLogger.Error(err, "failed to parse GitHub App auth, retrying with backoff", "backoff", backoff)
			if !waitWithContext(ctx, backoff) {
				return
			}
			continue
		}

		factory := opts.ScaleSetFactory
		if factory == nil {
			factory = githubscaleset.NewScaleSetClientFactory()
		}

		ghaClient, err := factory.NewClient(scaleSet.Spec.GitHub.ConfigURL, auth)
		if err != nil {
			backoff := calculateBackoff(attempt, 2*time.Second, 60*time.Second)
			attempt++
			runnerLogger.Error(err, "failed to create scale set client, retrying with backoff", "backoff", backoff)
			if !waitWithContext(ctx, backoff) {
				return
			}
			continue
		}

		tracker.SetGitHubAuthenticated(true)

		scaler := NewScalerHandler(opts.K8sClient, opts.Namespace, opts.Name, tracker)
		recorder := githubscaleset.NewMetricsRecorder(opts.K8sClient, opts.Namespace, opts.Name)
		maxCapacity := int(scaleSet.Status.EffectiveMaxRunners)
		if scaleSet.Spec.Suspend {
			maxCapacity = 0
		}

		l, err := ghaClient.CreateListener(ctx, scaleSet.Status.ScaleSetID, maxCapacity, scaler, recorder)
		if err != nil {
			backoff := calculateBackoff(attempt, 2*time.Second, 60*time.Second)
			attempt++
			runnerLogger.Error(err, "failed to initialize listener, retrying with backoff", "backoff", backoff)
			if !waitWithContext(ctx, backoff) {
				return
			}
			continue
		}

		tracker.SetSessionEstablished(true)

		// バックグラウンドでRunnerScaleSetのStatus.EffectiveMaxRunnersを監視し、動的にSetMaxRunnersを呼び出す
		sessionCtx, cancel := context.WithCancel(ctx)
		go func(currentMax int) {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			lastMax := currentMax
			for {
				select {
				case <-sessionCtx.Done():
					return
				case <-ticker.C:
					var currentSS ghav1alpha1.RunnerScaleSet
					if err := opts.K8sClient.Get(sessionCtx, client.ObjectKey{Namespace: opts.Namespace, Name: opts.Name}, &currentSS); err == nil {
						newMax := int(currentSS.Status.EffectiveMaxRunners)
						if currentSS.Spec.Suspend {
							newMax = 0
						}
						if newMax != lastMax {
							runnerLogger.Info("updating advertised maxCapacity on listener", "oldMax", lastMax, "newMax", newMax)
							l.SetMaxRunners(newMax)
							lastMax = newMax
						}
					}
				}
			}
		}(maxCapacity)

		sessionStartTime := time.Now()
		runnerLogger.Info("starting GitHub actions scale set listener session...", "maxCapacity", maxCapacity)
		if err := l.Run(sessionCtx, scaler); err != nil {
			cancel()
			metrics.ListenerSessionUp.WithLabelValues(opts.Namespace, opts.Name).Set(0)
			if time.Since(sessionStartTime) > 30*time.Second {
				attempt = 0 // 正常に30秒以上稼働していたらattemptをリセット
			}
			backoff := calculateBackoff(attempt, 1*time.Second, 60*time.Second)
			attempt++
			runnerLogger.Error(err, "listener session ended with error, restarting with backoff", "backoff", backoff)
			if !waitWithContext(ctx, backoff) {
				return
			}
		} else {
			cancel()
			metrics.ListenerSessionUp.WithLabelValues(opts.Namespace, opts.Name).Set(0)
			attempt = 0
		}
	}
}
