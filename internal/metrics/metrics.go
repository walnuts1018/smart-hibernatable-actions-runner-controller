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
	// MachinePoweredOn measures whether the physical machine is currently powered on (1 for on, 0 for off).
	MachinePoweredOn = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_baremetal_machine_powered_on",
			Help: "Whether the physical machine is currently powered on (1 for on, 0 for off).",
		},
		[]string{labelNamespace, labelName},
	)
	// ColdStartSeconds observes time taken from machine power on until Kubernetes node Ready.
	ColdStartSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gha_baremetal_cold_start_seconds",
			Help:    "Time in seconds taken from machine power on until Kubernetes node Ready.",
			Buckets: []float64{30, 60, 90, 120, 180, 240, 300, 420, 600, 900},
		},
		[]string{labelNamespace, labelName},
	)
	// RunnerProvisionSeconds observes time taken to provision and start an ephemeral runner pod.
	RunnerProvisionSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gha_baremetal_runner_provision_seconds",
			Help:    "Time in seconds taken to provision and start an ephemeral runner pod.",
			Buckets: []float64{1, 2, 5, 10, 20, 30, 60, 120},
		},
		[]string{labelNamespace, labelName},
	)
	// JobQueueToStartedObservedSeconds observes end-to-end duration from GitHub Job QueueTime until JobStarted observed.
	JobQueueToStartedObservedSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gha_baremetal_job_queue_to_started_observed_seconds",
			Help:    "Duration in seconds from GitHub actions job QueueTime until JobStarted is observed by SHARC.",
			Buckets: []float64{5, 15, 30, 60, 120, 180, 300, 600, 900},
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
	// CapacityDemand measures total aggregated runner demand for the node pool.
	CapacityDemand = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_runner_capacity_demand",
			Help: "Total aggregated runner demand across all referencing scale sets.",
		},
		[]string{labelNamespace, labelName},
	)
	// CapacityCommitted measures total committed runner capacity (Ready + PoweringOn).
	CapacityCommitted = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_runner_capacity_committed",
			Help: "Total committed runner capacity (Ready + PoweringOn selected machines).",
		},
		[]string{labelNamespace, labelName},
	)
	// CapacityReady measures currently ready and schedulable runner capacity.
	CapacityReady = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_runner_capacity_ready",
			Help: "Currently ready and schedulable runner capacity.",
		},
		[]string{labelNamespace, labelName},
	)
	// CapacityDeficit measures deficit of ready runner capacity compared to desired runners.
	CapacityDeficit = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_runner_capacity_deficit",
			Help: "Deficit of ready runner capacity compared to desired runners (demand - ready, clamped to >= 0).",
		},
		[]string{labelNamespace, labelName},
	)
	// UncommittedDeficit measures demand that is not yet covered by committed capacity.
	UncommittedDeficit = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gha_runner_capacity_uncommitted_deficit",
			Help: "Deficit of committed capacity compared to demand (demand - committed, clamped to >= 0).",
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
		MachinePoweredOn,
		PowerTransitionsTotal,
		ClusterAPIReachable,
		ColdStartSeconds,
		RunnerProvisionSeconds,
		JobQueueToStartedObservedSeconds,
		ListenerSessionUp,
		ListenerLastSuccessfulPoll,
		CapacityDemand,
		CapacityCommitted,
		CapacityReady,
		CapacityDeficit,
		UncommittedDeficit,
		EffectiveMaxRunners,
		RedfishRequestsTotal,
		RedfishRequestDuration,
	)
}
