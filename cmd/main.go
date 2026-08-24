/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ghav1alpha1 "github.com/walnuts1018/smart-hibernatable-actions-runner-controller/api/v1alpha1"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/capacity"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/controller"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/githubscaleset"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/logger"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/metrics"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/redfish"
	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/remotecluster"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(ghav1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var listenerImage string
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address used by the OpenTelemetry Prometheus exporter.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&listenerImage, "listener-image", "ghcr.io/walnuts1018/smart-hibernatable-actions-runner-controller/listener:latest", "The image to use for the listener deployment.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	var logLevelStr string
	var logTypeStr string
	flag.StringVar(&logLevelStr, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&logTypeStr, "log-type", "json", "Log type (json, text)")
	flag.Parse()

	logger.Setup(logLevelStr, logTypeStr)
	ctx := ctrl.SetupSignalHandler()
	if err := metrics.Setup(ctx, "sharc-manager"); err != nil {
		setupLog.Error(err, "failed to configure OpenTelemetry metrics")
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := metrics.Shutdown(shutdownCtx); err != nil {
			setupLog.Error(err, "failed to shut down OpenTelemetry metrics")
		}
	}()
	go func() {
		if err := metrics.Serve(ctx, metricsAddr); err != nil {
			setupLog.Error(err, "OpenTelemetry Prometheus endpoint failed")
		}
	}()

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	metricsServerOptions := metricsserver.Options{
		BindAddress: "0",
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "abc7dfe6.walnuts.dev",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// Setup FieldIndexers
	if err := controller.SetupIndexesWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup field indexer")
		os.Exit(1)
	}

	// Initialize adapters and providers
	remoteProvider := remotecluster.NewProvider(mgr.GetClient(), mgr.GetScheme())
	planner := capacity.NewOrderedCapacityPlanner(false)
	redfishFactory := redfish.NewGofishControllerFactory()
	scaleSetFactory := githubscaleset.NewScaleSetClientFactory()

	if err = (&controller.RunnerClusterReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		Recorder:       mgr.GetEventRecorder("runnercluster-controller"),
		RemoteProvider: remoteProvider,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RunnerCluster")
		os.Exit(1)
	}

	if err = (&controller.RunnerMachineReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		Recorder:       mgr.GetEventRecorder("runnermachine-controller"),
		RemoteProvider: remoteProvider,
		RedfishFactory: redfishFactory,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RunnerMachine")
		os.Exit(1)
	}

	if err = (&controller.RunnerNodePoolReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Recorder:        mgr.GetEventRecorder("runnernodepool-controller"),
		RemoteProvider:  remoteProvider,
		Planner:         planner,
		EnableMultiNode: false,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RunnerNodePool")
		os.Exit(1)
	}

	if err = (&controller.RunnerScaleSetReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Recorder:        mgr.GetEventRecorder("runnerscaleset-controller"),
		ScaleSetFactory: scaleSetFactory,
		ListenerImage:   listenerImage,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RunnerScaleSet")
		os.Exit(1)
	}

	if err = (&controller.EphemeralRunnerSetReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("ephemeralrunnerset-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "EphemeralRunnerSet")
		os.Exit(1)
	}

	if err = (&controller.EphemeralRunnerReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Recorder:        mgr.GetEventRecorder("ephemeralrunner-controller"),
		RemoteProvider:  remoteProvider,
		ScaleSetFactory: scaleSetFactory,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "EphemeralRunner")
		os.Exit(1)
	}

	// Register Webhooks
	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		if err = (&ghav1alpha1.RunnerScaleSet{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "RunnerScaleSet")
			os.Exit(1)
		}
		if err = (&ghav1alpha1.RunnerCluster{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "RunnerCluster")
			os.Exit(1)
		}
		if err = (&ghav1alpha1.RunnerNodePool{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "RunnerNodePool")
			os.Exit(1)
		}
		if err = (&ghav1alpha1.RunnerMachine{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "RunnerMachine")
			os.Exit(1)
		}
		if err = (&ghav1alpha1.EphemeralRunnerSet{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "EphemeralRunnerSet")
			os.Exit(1)
		}
		if err = (&ghav1alpha1.EphemeralRunner{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "EphemeralRunner")
			os.Exit(1)
		}
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
