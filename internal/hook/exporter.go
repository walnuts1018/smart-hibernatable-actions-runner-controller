package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// MetricValues contains the calculated job execution metrics.
type MetricValues struct {
	PeakMemoryBytes uint64
	AvgCPUCores     float64
	DurationSeconds int64
	JobName         string
	WorkflowName    string
	Repository      string
	JobResult       string
	Namespace       string
	PodName         string
	NodeName        string
	ExtraAttributes map[string]string
}

// BuildOtlpPayload constructs the OpenTelemetry OTLP JSON payload for the runner metrics.
func BuildOtlpPayload(mv MetricValues, now time.Time) OtlpPayload {
	nowNano := strconv.FormatInt(now.UnixNano(), 10)

	// Resource attributes
	resAttrs := []OtlpAttribute{
		{Key: "service.name", Value: OtlpStringValue{StringValue: "actions-runner"}},
	}
	if mv.Namespace != "" {
		resAttrs = append(resAttrs, OtlpAttribute{Key: "k8s.namespace.name", Value: OtlpStringValue{StringValue: mv.Namespace}})
	}
	if mv.PodName != "" {
		resAttrs = append(resAttrs, OtlpAttribute{Key: "k8s.pod.name", Value: OtlpStringValue{StringValue: mv.PodName}})
	}
	if mv.NodeName != "" {
		resAttrs = append(resAttrs, OtlpAttribute{Key: "k8s.node.name", Value: OtlpStringValue{StringValue: mv.NodeName}})
	}

	// Repository short name
	repoShort := mv.Repository
	if idx := strings.LastIndex(repoShort, "/"); idx != -1 {
		repoShort = repoShort[idx+1:]
	}

	// DataPoint attributes
	dpAttrs := []OtlpAttribute{
		{Key: "job_name", Value: OtlpStringValue{StringValue: mv.JobName}},
		{Key: "job_workflow_name", Value: OtlpStringValue{StringValue: mv.WorkflowName}},
		{Key: "repository", Value: OtlpStringValue{StringValue: repoShort}},
	}
	if mv.JobResult != "" {
		dpAttrs = append(dpAttrs, OtlpAttribute{Key: "job_result", Value: OtlpStringValue{StringValue: mv.JobResult}})
	}
	for k, v := range mv.ExtraAttributes {
		dpAttrs = append(dpAttrs, OtlpAttribute{Key: k, Value: OtlpStringValue{StringValue: v}})
	}

	peakMemInt := int64(mv.PeakMemoryBytes)
	durationInt := mv.DurationSeconds
	avgCPU := mv.AvgCPUCores

	metrics := []OtlpMetric{
		{
			Name: "gha_job_resource_peak_memory_bytes",
			Gauge: OtlpGauge{
				DataPoints: []OtlpDataPoint{
					{
						AsInt:        &peakMemInt,
						TimeUnixNano: nowNano,
						Attributes:   dpAttrs,
					},
				},
			},
		},
		{
			Name: "gha_job_resource_avg_cpu_cores",
			Gauge: OtlpGauge{
				DataPoints: []OtlpDataPoint{
					{
						AsDouble:     &avgCPU,
						TimeUnixNano: nowNano,
						Attributes:   dpAttrs,
					},
				},
			},
		},
		{
			Name: "gha_job_resource_duration_seconds",
			Gauge: OtlpGauge{
				DataPoints: []OtlpDataPoint{
					{
						AsInt:        &durationInt,
						TimeUnixNano: nowNano,
						Attributes:   dpAttrs,
					},
				},
			},
		},
	}

	return OtlpPayload{
		ResourceMetrics: []OtlpResourceMetrics{
			{
				Resource: OtlpResource{
					Attributes: resAttrs,
				},
				ScopeMetrics: []OtlpScopeMetrics{
					{
						Scope: OtlpScope{
							Name: "github.actions.runner",
						},
						Metrics: metrics,
					},
				},
			},
		},
	}
}

// SendOtlpMetrics sends the OTLP payload to the given endpoint via HTTP POST.
func SendOtlpMetrics(ctx context.Context, endpoint string, payload OtlpPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal OTLP payload: %w", err)
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*200) * time.Millisecond)
		}

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if reqErr != nil {
			return fmt.Errorf("failed to create http request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, doErr := client.Do(req)
		if doErr != nil {
			lastErr = doErr
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("unexpected status code from metrics collector: %d", resp.StatusCode)
	}

	return lastErr
}

// ParseExtraAttributes parses JSON-encoded extra attributes string if present.
func ParseExtraAttributes(envVal string) map[string]string {
	if envVal == "" {
		return nil
	}
	var attrs map[string]string
	if err := json.Unmarshal([]byte(envVal), &attrs); err != nil {
		// Try key=value,key=value comma-separated format
		attrs = make(map[string]string)
		pairs := strings.Split(envVal, ",")
		for _, pair := range pairs {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 {
				attrs[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}
	return attrs
}

// PopulateMetricValues fills in metadata from current environment variables.
func PopulateMetricValues(mv *MetricValues) {
	if mv.JobName == "" {
		mv.JobName = os.Getenv("GITHUB_JOB")
		if mv.JobName == "" {
			mv.JobName = "unknown"
		}
	}
	if mv.WorkflowName == "" {
		mv.WorkflowName = os.Getenv("GITHUB_WORKFLOW")
		if mv.WorkflowName == "" {
			mv.WorkflowName = "unknown"
		}
	}
	if mv.Repository == "" {
		mv.Repository = os.Getenv("GITHUB_REPOSITORY")
		if mv.Repository == "" {
			mv.Repository = "unknown"
		}
	}
	if mv.JobResult == "" {
		mv.JobResult = os.Getenv("GITHUB_JOB_STATUS")
	}
	if mv.Namespace == "" {
		mv.Namespace = os.Getenv("POD_NAMESPACE")
	}
	if mv.PodName == "" {
		mv.PodName = os.Getenv("POD_NAME")
	}
	if mv.NodeName == "" {
		mv.NodeName = os.Getenv("NODE_NAME")
	}
	if mv.ExtraAttributes == nil {
		mv.ExtraAttributes = ParseExtraAttributes(os.Getenv("RUNNER_METRICS_EXTRA_ATTRIBUTES"))
	}
}
