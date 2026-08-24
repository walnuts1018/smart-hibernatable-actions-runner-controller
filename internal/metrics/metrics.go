package metrics

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

const instrumentationName = "github.com/walnuts1018/smart-hibernatable-actions-runner-controller"

type gaugeVec struct {
	mu     sync.RWMutex
	values map[string]gaugeValue
	labels []string
}
type gaugeValue struct {
	value float64
	attrs []attribute.KeyValue
}
type gauge struct {
	vec   *gaugeVec
	key   string
	attrs []attribute.KeyValue
}

func newGaugeVec(m otelmetric.Meter, name, help string, labels ...string) *gaugeVec {
	g := &gaugeVec{values: map[string]gaugeValue{}, labels: labels}
	instrument, err := m.Float64ObservableGauge(name, otelmetric.WithDescription(help))
	if err != nil {
		panic(err)
	}
	if _, err = m.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		g.mu.RLock()
		defer g.mu.RUnlock()
		for _, v := range g.values {
			observer.ObserveFloat64(instrument, v.value, otelmetric.WithAttributes(v.attrs...))
		}
		return nil
	}, instrument); err != nil {
		panic(err)
	}
	return g
}
func (g *gaugeVec) WithLabelValues(values ...string) *gauge {
	attrs, key := g.attributes(values)
	return &gauge{g, key, attrs}
}
func (g *gaugeVec) DeleteLabelValues(values ...string) {
	_, key := g.attributes(values)
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.values, key)
}
func (g *gaugeVec) attributes(values []string) ([]attribute.KeyValue, string) {
	return makeAttributes(g.labels, values), strings.Join(values, "\x00")
}
func (g *gauge) Set(value float64) {
	g.vec.mu.Lock()
	defer g.vec.mu.Unlock()
	g.vec.values[g.key] = gaugeValue{value, g.attrs}
}

type counterVec struct {
	instrument otelmetric.Float64Counter
	labels     []string
}
type counter struct {
	instrument otelmetric.Float64Counter
	attrs      []attribute.KeyValue
}

func newCounterVec(m otelmetric.Meter, name, help string, labels ...string) *counterVec {
	i, err := m.Float64Counter(name, otelmetric.WithDescription(help))
	if err != nil {
		panic(err)
	}
	return &counterVec{i, labels}
}
func (c *counterVec) WithLabelValues(values ...string) *counter {
	return &counter{c.instrument, makeAttributes(c.labels, values)}
}
func (c *counter) Inc(ctx context.Context) {
	c.instrument.Add(ctx, 1, otelmetric.WithAttributes(c.attrs...))
}

type histogramVec struct {
	instrument otelmetric.Float64Histogram
	labels     []string
}
type histogram struct {
	instrument otelmetric.Float64Histogram
	attrs      []attribute.KeyValue
}

func newHistogramVec(m otelmetric.Meter, name, help string, buckets []float64, labels ...string) *histogramVec {
	i, err := m.Float64Histogram(name, otelmetric.WithDescription(help), otelmetric.WithExplicitBucketBoundaries(buckets...))
	if err != nil {
		panic(err)
	}
	return &histogramVec{i, labels}
}
func (h *histogramVec) WithLabelValues(values ...string) *histogram {
	return &histogram{h.instrument, makeAttributes(h.labels, values)}
}
func (h *histogram) Observe(ctx context.Context, value float64) {
	h.instrument.Record(ctx, value, otelmetric.WithAttributes(h.attrs...))
}

func makeAttributes(names, values []string) []attribute.KeyValue {
	if len(names) != len(values) {
		panic(fmt.Sprintf("metric expected %d label values, got %d", len(names), len(values)))
	}
	attrs := make([]attribute.KeyValue, len(values))
	for i := range values {
		attrs[i] = attribute.String(names[i], values[i])
	}
	return attrs
}

var (
	AvailableJobs, AcquiredJobs, AssignedJobs, RunningJobs                                                     *gaugeVec
	RegisteredRunners, BusyRunners, DesiredRunners, IdleRunners                                                *gaugeVec
	DesiredNodes, PoweredOnNodes, ReadyNodes, MachinePowerState                                                *gaugeVec
	ClusterAPIReachable, MachinePoweredOn, ListenerSessionUp, ListenerLastSuccessfulPoll                       *gaugeVec
	CapacityDemand, CapacityCommitted, CapacityReady, CapacityDeficit, UncommittedDeficit, EffectiveMaxRunners *gaugeVec
	PowerTransitionsTotal, RedfishRequestsTotal                                                                *counterVec
	ColdStartSeconds, RunnerProvisionSeconds, JobQueueToStartedObservedSeconds, RedfishRequestDuration         *histogramVec
	provider                                                                                                   *sdkmetric.MeterProvider
	handler                                                                                                    http.Handler
)

func init() {
	register(otel.GetMeterProvider().Meter(instrumentationName))
}

// Setup configures metrics from standard OpenTelemetry environment variables.
func Setup(ctx context.Context, serviceName string) error {
	handler = nil
	exporterName := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_METRICS_EXPORTER")))
	if exporterName == "" {
		exporterName = "none"
	}
	if strings.Contains(exporterName, ",") {
		return fmt.Errorf("OTEL_METRICS_EXPORTER must select exactly one exporter: %q", exporterName)
	}
	var reader sdkmetric.Reader
	switch exporterName {
	case "none":
	case "prometheus":
		registry := prometheus.NewRegistry()
		exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(registry), otelprometheus.WithoutScopeInfo())
		if err != nil {
			return fmt.Errorf("create Prometheus metric exporter: %w", err)
		}
		reader, handler = exporter, promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	case "otlp":
		exporter, err := newOTLPExporter(ctx)
		if err != nil {
			return fmt.Errorf("create OTLP metric exporter: %w", err)
		}
		interval, err := exportInterval()
		if err != nil {
			return err
		}
		reader = sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval))
	default:
		return fmt.Errorf("unsupported OTEL_METRICS_EXPORTER %q", exporterName)
	}
	opts := []sdkmetric.Option{sdkmetric.WithResource(metricResource(serviceName))}
	if reader != nil {
		opts = append(opts, sdkmetric.WithReader(reader))
	}
	provider = sdkmetric.NewMeterProvider(opts...)
	otel.SetMeterProvider(provider)
	register(provider.Meter(instrumentationName))
	return nil
}

func newOTLPExporter(ctx context.Context) (sdkmetric.Exporter, error) {
	protocol := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"))
	if protocol == "" {
		protocol = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	}
	switch protocol {
	case "", "grpc":
		return otlpmetricgrpc.New(ctx)
	case "http/protobuf":
		return otlpmetrichttp.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP metrics protocol %q", protocol)
	}
}

func metricResource(serviceName string) *resource.Resource {
	base := resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName))
	merged, err := resource.Merge(base, resource.Default())
	if err != nil {
		return base
	}
	return merged
}
func exportInterval() (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv("OTEL_METRIC_EXPORT_INTERVAL"))
	if value == "" {
		return 60 * time.Second, nil
	}
	ms, err := strconv.ParseInt(value, 10, 64)
	if err != nil || ms <= 0 {
		return 0, fmt.Errorf("invalid OTEL_METRIC_EXPORT_INTERVAL %q", value)
	}
	return time.Duration(ms) * time.Millisecond, nil
}
func Handler() http.Handler { return handler }
func Shutdown(ctx context.Context) error {
	if provider == nil {
		return nil
	}
	return provider.Shutdown(ctx)
}

// Serve exposes the Prometheus exporter using the OpenTelemetry Prometheus host and port environment variables.
func Serve(ctx context.Context) error {
	if handler == nil {
		return nil
	}
	host := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_PROMETHEUS_HOST"))
	if host == "" {
		host = "localhost"
	}
	port := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_PROMETHEUS_PORT"))
	if port == "" {
		port = "9464"
	}
	address := net.JoinHostPort(host, port)
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func register(m otelmetric.Meter) {
	const ns, name = "namespace", "name"
	AvailableJobs = newGaugeVec(m, "gha_available_jobs", "Number of jobs currently available in the queue for the runner scale set.", ns, name)
	AcquiredJobs = newGaugeVec(m, "gha_acquired_jobs", "Number of jobs acquired by the runner scale set.", ns, name)
	AssignedJobs = newGaugeVec(m, "gha_assigned_jobs", "Number of jobs currently assigned to the runner scale set.", ns, name)
	RunningJobs = newGaugeVec(m, "gha_running_jobs", "Number of jobs currently running on the runner scale set.", ns, name)
	RegisteredRunners = newGaugeVec(m, "gha_registered_runners", "Number of runners currently registered in GitHub Actions.", ns, name)
	BusyRunners = newGaugeVec(m, "gha_busy_runners", "Number of registered runners currently executing a job.", ns, name)
	DesiredRunners = newGaugeVec(m, "gha_desired_runners", "Target number of runners desired for the scale set.", ns, name)
	IdleRunners = newGaugeVec(m, "gha_idle_runners", "Number of idle runners registered in GitHub Actions.", ns, name)
	DesiredNodes = newGaugeVec(m, "gha_baremetal_desired_nodes", "Number of desired physical nodes for the node pool.", ns, name)
	PoweredOnNodes = newGaugeVec(m, "gha_baremetal_powered_on_nodes", "Number of powered on physical nodes in the node pool.", ns, name)
	ReadyNodes = newGaugeVec(m, "gha_baremetal_ready_nodes", "Number of ready physical nodes in the node pool.", ns, name)
	MachinePowerState = newGaugeVec(m, "gha_baremetal_machine_power_state", "Current power state of the machine (1 for current state, 0 otherwise).", ns, name, "state")
	MachinePoweredOn = newGaugeVec(m, "gha_baremetal_machine_powered_on", "Whether the physical machine is currently powered on (1 for on, 0 for off).", ns, name)
	ClusterAPIReachable = newGaugeVec(m, "gha_baremetal_cluster_api_reachable", "Reachability of the runner cluster API (1 if reachable, 0 otherwise).", ns, name)
	ListenerSessionUp = newGaugeVec(m, "gha_listener_session_up", "Whether the scale set listener message session is currently established (1 for up, 0 for down).", ns, name)
	ListenerLastSuccessfulPoll = newGaugeVec(m, "gha_listener_last_successful_poll_timestamp_seconds", "Unix timestamp of the latest successful poll from GitHub Actions message session.", ns, name)
	CapacityDemand = newGaugeVec(m, "gha_runner_capacity_demand", "Total aggregated runner demand across all referencing scale sets.", ns, name)
	CapacityCommitted = newGaugeVec(m, "gha_runner_capacity_committed", "Total committed runner capacity (Ready + PoweringOn selected machines).", ns, name)
	CapacityReady = newGaugeVec(m, "gha_runner_capacity_ready", "Currently ready and schedulable runner capacity.", ns, name)
	CapacityDeficit = newGaugeVec(m, "gha_runner_capacity_deficit", "Deficit of ready runner capacity compared to desired runners.", ns, name)
	UncommittedDeficit = newGaugeVec(m, "gha_runner_capacity_uncommitted_deficit", "Deficit of committed capacity compared to demand.", ns, name)
	EffectiveMaxRunners = newGaugeVec(m, "gha_effective_max_runners", "Current calculated effective maximum runners capacity for the scale set.", ns, name)
	PowerTransitionsTotal = newCounterVec(m, "gha_baremetal_power_transitions_total", "Total number of machine power transitions initiated.", ns, name, "action")
	RedfishRequestsTotal = newCounterVec(m, "gha_redfish_requests_total", "Total number of Redfish BMC API requests executed.", ns, name, "operation", "result")
	ColdStartSeconds = newHistogramVec(m, "gha_baremetal_cold_start_seconds", "Time in seconds taken from machine power on until Kubernetes node Ready.", []float64{30, 60, 90, 120, 180, 240, 300, 420, 600, 900}, ns, name)
	RunnerProvisionSeconds = newHistogramVec(m, "gha_baremetal_runner_provision_seconds", "Time in seconds taken to provision and start an ephemeral runner pod.", []float64{1, 2, 5, 10, 20, 30, 60, 120}, ns, name)
	JobQueueToStartedObservedSeconds = newHistogramVec(m, "gha_baremetal_job_queue_to_started_observed_seconds", "Duration in seconds from GitHub Actions job QueueTime until JobStarted is observed by SHARC.", []float64{5, 15, 30, 60, 120, 180, 300, 600, 900}, ns, name)
	RedfishRequestDuration = newHistogramVec(m, "gha_redfish_request_duration_seconds", "Duration in seconds of Redfish BMC API requests.", []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}, ns, name, "operation")
}
