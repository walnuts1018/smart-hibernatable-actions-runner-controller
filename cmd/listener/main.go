package main

import (
	"context"
	"flag"
	"os"
	"strings"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/listener"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/logger"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
)

var mainLog = ctrl.Log.WithName("listener-main")

func main() {
	var (
		scaleSetRefStr string
		probeAddr      string
		metricsAddr    string
		logLevelStr    string
		logTypeStr     string
	)

	flag.StringVar(&scaleSetRefStr, "runner-scale-set", "", "The namespace/name of the RunnerScaleSet to listen for.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to.")
	flag.StringVar(&logLevelStr, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&logTypeStr, "log-type", "json", "Log type (json, text)")
	flag.Parse()

	logger.Setup(logLevelStr, logTypeStr)

	if scaleSetRefStr == "" {
		mainLog.Error(nil, "--runner-scale-set flag is required (format: namespace/name)")
		os.Exit(1)
	}

	parts := strings.Split(scaleSetRefStr, "/")
	if len(parts) != 2 {
		mainLog.Error(nil, "invalid --runner-scale-set format. Expected namespace/name")
		os.Exit(1)
	}
	namespace, name := parts[0], parts[1]

	runtimeScheme := clientgoscheme.Scheme
	utilruntime.Must(ghav1alpha1.AddToScheme(runtimeScheme))

	config := ctrl.GetConfigOrDie()
	k8sClient, err := client.New(config, client.Options{Scheme: runtimeScheme})
	if err != nil {
		mainLog.Error(err, "failed to create k8s client")
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		mainLog.Error(err, "failed to create kubernetes clientset")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()
	if err := metrics.Setup(ctx, "sharc-listener"); err != nil {
		mainLog.Error(err, "failed to configure OpenTelemetry metrics")
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := metrics.Shutdown(shutdownCtx); err != nil {
			mainLog.Error(err, "failed to shut down OpenTelemetry metrics")
		}
	}()

	runnerOpts := listener.RunnerOptions{
		Namespace:   namespace,
		Name:        name,
		ProbeAddr:   probeAddr,
		MetricsAddr: metricsAddr,
		Config:      config,
		K8sClient:   k8sClient,
		Clientset:   clientset,
	}

	if err := listener.RunListenerWithLease(ctx, runnerOpts); err != nil {
		mainLog.Error(err, "failed to run listener")
		os.Exit(1)
	}
}
