package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	labelNamespace = "namespace"
	labelName      = "name"
)

var (
	// AssignedJobs measures the number of jobs currently assigned to the runner scale set.
	AssignedJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_assigned_jobs",
			Help: "Number of jobs currently assigned to the runner scale set.",
		},
		[]string{labelNamespace, labelName},
	)
	// RunningJobs measures the number of jobs currently running on the runner scale set.
	RunningJobs = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_running_jobs",
			Help: "Number of jobs currently running on the runner scale set.",
		},
		[]string{labelNamespace, labelName},
	)
	// RegisteredRunners measures the number of runners currently registered in GitHub Actions.
	RegisteredRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_registered_runners",
			Help: "Number of runners currently registered in GitHub Actions.",
		},
		[]string{labelNamespace, labelName},
	)
	// BusyRunners measures the number of registered runners currently executing a job.
	BusyRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_busy_runners",
			Help: "Number of registered runners currently executing a job.",
		},
		[]string{labelNamespace, labelName},
	)
	// DesiredRunners measures the target number of runners desired for the scale set.
	DesiredRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_desired_runners",
			Help: "Target number of runners desired for the scale set.",
		},
		[]string{labelNamespace, labelName},
	)
	// IdleRunners measures the number of idle runners registered in GitHub Actions.
	IdleRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_idle_runners",
			Help: "Number of idle runners registered in GitHub Actions.",
		},
		[]string{labelNamespace, labelName},
	)

	// DesiredNodes measures the number of desired physical nodes for the node pool.
	DesiredNodes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_desired_nodes",
			Help: "Number of desired physical nodes for the node pool.",
		},
		[]string{labelNamespace, labelName},
	)
	// PoweredOnNodes measures the number of powered on physical nodes in the node pool.
	PoweredOnNodes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_powered_on_nodes",
			Help: "Number of powered on physical nodes in the node pool.",
		},
		[]string{labelNamespace, labelName},
	)
	// ReadyNodes measures the number of ready physical nodes in the node pool.
	ReadyNodes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_ready_nodes",
			Help: "Number of ready physical nodes in the node pool.",
		},
		[]string{labelNamespace, labelName},
	)
	// MachinePowerState measures the current power state of the machine.
	MachinePowerState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_machine_power_state",
			Help: "Current power state of the machine (1 for current state, 0 otherwise).",
		},
		[]string{labelNamespace, labelName, "state"},
	)
	// PowerTransitionsTotal counts total machine power transitions initiated.
	PowerTransitionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gha_baremetal_power_transitions_total",
			Help: "Total number of machine power transitions initiated.",
		},
		[]string{labelNamespace, labelName, "action"},
	)
	// ClusterAPIReachable measures reachability of the runner cluster API.
	ClusterAPIReachable = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_cluster_api_reachable",
			Help: "Reachability of the runner cluster API (1 if reachable, 0 otherwise).",
		},
		[]string{labelNamespace, labelName},
	)
	// ColdStartSeconds observes time taken from machine power on until Kubernetes node Ready.
	ColdStartSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gha_baremetal_cold_start_seconds",
			Help:    "Time in seconds taken from machine power on until Kubernetes node Ready.",
			Buckets: prometheus.ExponentialBuckets(10, 1.5, 10),
		},
		[]string{labelNamespace, labelName},
	)
	// RunnerProvisionSeconds observes time taken to provision and start an ephemeral runner pod.
	RunnerProvisionSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gha_baremetal_runner_provision_seconds",
			Help:    "Time in seconds taken to provision and start an ephemeral runner pod.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 8),
		},
		[]string{labelNamespace, labelName},
	)

	// ListenerSessionUp measures whether the scale set listener message session is established.
	ListenerSessionUp = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_listener_session_up",
			Help: "Whether the scale set listener message session is currently established (1 for up, 0 for down).",
		},
		[]string{labelNamespace, labelName},
	)
	// ListenerLastSuccessfulPoll records unix timestamp of latest successful poll.
	ListenerLastSuccessfulPoll = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_listener_last_successful_poll_timestamp_seconds",
			Help: "Unix timestamp of the latest successful poll from GitHub Actions message session.",
		},
		[]string{labelNamespace, labelName},
	)
	// CapacityDeficit measures deficit of ready runner capacity compared to desired runners.
	CapacityDeficit = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_runner_capacity_deficit",
			Help: "Deficit of ready runner capacity compared to desired runners (desired - ready, clamped to >= 0).",
		},
		[]string{labelNamespace, labelName},
	)
	// EffectiveMaxRunners measures current calculated effective maximum runners capacity.
	EffectiveMaxRunners = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_effective_max_runners",
			Help: "Current calculated effective maximum runners capacity for the scale set.",
		},
		[]string{labelNamespace, labelName},
	)
	// RedfishRequestsTotal counts total Redfish BMC API requests executed.
	RedfishRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gha_redfish_requests_total",
			Help: "Total number of Redfish BMC API requests executed.",
		},
		[]string{labelNamespace, labelName, "operation", "result"},
	)
	// RedfishRequestDuration observes duration in seconds of Redfish BMC API requests.
	RedfishRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gha_redfish_request_duration_seconds",
			Help:    "Duration in seconds of Redfish BMC API requests.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{labelNamespace, labelName, "operation"},
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
