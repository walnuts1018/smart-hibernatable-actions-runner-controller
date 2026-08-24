package hook

import "time"

const DefaultUnknownValue = "unknown"

// JobStartData holds state captured at job start time and saved to a state file.
type JobStartData struct {
	StartTime         time.Time `json:"startTime"`
	StartCPUUsageUsec uint64    `json:"startCpuUsageUsec"`
	StartMemoryPeak   uint64    `json:"startMemoryPeak"`
	CgroupPath        string    `json:"cgroupPath"`
}

// OTLP Metric JSON structures for HTTP POST to OpenTelemetry Collector.

type OtlpPayload struct {
	ResourceMetrics []OtlpResourceMetrics `json:"resourceMetrics"`
}

type OtlpResourceMetrics struct {
	Resource     OtlpResource       `json:"resource"`
	ScopeMetrics []OtlpScopeMetrics `json:"scopeMetrics"`
}

type OtlpResource struct {
	Attributes []OtlpAttribute `json:"attributes"`
}

type OtlpScopeMetrics struct {
	Scope   OtlpScope    `json:"scope"`
	Metrics []OtlpMetric `json:"metrics"`
}

type OtlpScope struct {
	Name string `json:"name"`
}

type OtlpMetric struct {
	Name  string    `json:"name"`
	Gauge OtlpGauge `json:"gauge"`
}

type OtlpGauge struct {
	DataPoints []OtlpDataPoint `json:"dataPoints"`
}

type OtlpDataPoint struct {
	AsInt        *int64          `json:"asInt,omitempty"`
	AsDouble     *float64        `json:"asDouble,omitempty"`
	TimeUnixNano string          `json:"timeUnixNano"`
	Attributes   []OtlpAttribute `json:"attributes"`
}

type OtlpAttribute struct {
	Key   string          `json:"key"`
	Value OtlpStringValue `json:"value"`
}

type OtlpStringValue struct {
	StringValue string `json:"stringValue"`
}
