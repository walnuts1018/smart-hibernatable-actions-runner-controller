package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// Scale set metrics
	AssignedJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_assigned_jobs",
			Help: "Number of jobs currently assigned to the runner scale set.",
		},
		[]string{"namespace", "name"},
	)
	RunningJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_running_jobs",
			Help: "Number of jobs currently running on the runner scale set.",
		},
		[]string{"namespace", "name"},
	)
	RegisteredRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_registered_runners",
			Help: "Number of runners currently registered in GitHub Actions.",
		},
		[]string{"namespace", "name"},
	)
	BusyRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_busy_runners",
			Help: "Number of registered runners currently executing a job.",
		},
		[]string{"namespace", "name"},
	)
	DesiredRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_desired_runners",
			Help: "Target number of runners desired for the scale set.",
		},
		[]string{"namespace", "name"},
	)
	IdleRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_idle_runners",
			Help: "Number of idle runners registered in GitHub Actions.",
		},
		[]string{"namespace", "name"},
	)

	// Baremetal operator metrics
	DesiredNodes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_desired_nodes",
			Help: "Number of desired physical nodes for the node pool.",
		},
		[]string{"namespace", "name"},
	)
	PoweredOnNodes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_powered_on_nodes",
			Help: "Number of powered on physical nodes in the node pool.",
		},
		[]string{"namespace", "name"},
	)
	ReadyNodes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_ready_nodes",
			Help: "Number of ready physical nodes in the node pool.",
		},
		[]string{"namespace", "name"},
	)
	MachinePowerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_machine_power_state",
			Help: "Current power state of the machine (1 for current state, 0 otherwise).",
		},
		[]string{"namespace", "name", "state"},
	)
	PowerTransitionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gha_baremetal_power_transitions_total",
			Help: "Total number of machine power transitions initiated.",
		},
		[]string{"namespace", "name", "action"},
	)
	ClusterAPIReachable = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_cluster_api_reachable",
			Help: "Reachability of the runner cluster API (1 if reachable, 0 otherwise).",
		},
		[]string{"namespace", "name"},
	)
	ColdStartSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gha_baremetal_cold_start_seconds",
			Help:    "Time in seconds taken from machine power on until Kubernetes node Ready.",
			Buckets: prometheus.ExponentialBuckets(10, 1.5, 10),
		},
		[]string{"namespace", "name"},
	)
	RunnerProvisionSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gha_baremetal_runner_provision_seconds",
			Help:    "Time in seconds taken to provision and start an ephemeral runner pod.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 8),
		},
		[]string{"namespace", "name"},
	)

	// Additional operational metrics
	ListenerSessionUp = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_listener_session_up",
			Help: "Whether the scale set listener message session is currently established (1 for up, 0 for down).",
		},
		[]string{"namespace", "name"},
	)
	ListenerLastSuccessfulPoll = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_listener_last_successful_poll_timestamp_seconds",
			Help: "Unix timestamp of the latest successful poll from GitHub Actions message session.",
		},
		[]string{"namespace", "name"},
	)
	CapacityDeficit = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_runner_capacity_deficit",
			Help: "Deficit of ready runner capacity compared to desired runners (desired - ready, clamped to >= 0).",
		},
		[]string{"namespace", "name"},
	)
	EffectiveMaxRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_effective_max_runners",
			Help: "Current calculated effective maximum runners capacity for the scale set.",
		},
		[]string{"namespace", "name"},
	)
	RedfishRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gha_redfish_requests_total",
			Help: "Total number of Redfish BMC API requests executed.",
		},
		[]string{"namespace", "name", "operation", "result"},
	)
	RedfishRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gha_redfish_request_duration_seconds",
			Help:    "Duration in seconds of Redfish BMC API requests.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"namespace", "name", "operation"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		AssignedJobs,
		RunningJobs,
		RegisteredRunners,
		BusyRunners,
		DesiredRunners,
		IdleRunners,
		DesiredNodes,
		PoweredOnNodes,
		ReadyNodes,
		MachinePowerState,
		PowerTransitionsTotal,
		ClusterAPIReachable,
		ColdStartSeconds,
		RunnerProvisionSeconds,
		ListenerSessionUp,
		ListenerLastSuccessfulPoll,
		CapacityDeficit,
		EffectiveMaxRunners,
		RedfishRequestsTotal,
		RedfishRequestDuration,
	)
}
