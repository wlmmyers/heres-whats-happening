// Package observability emits CloudWatch Embedded Metric Format (EMF) logs, so
// an operational signal becomes a metric on ingestion without a metric-filter
// pattern string.
package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Metric contract. These are referenced verbatim by the CloudWatch alarms in
// terraform/prod/observability.tf; changing any without updating the alarms
// makes those alarms go blind. TestMetricContractConstants guards the values.
const (
	Namespace                 = "HeresWhatsHappening/api"
	MetricRateLimitRejections = "RateLimitRejections"
	DimensionEndpoint         = "endpoint"
)

// Emitter writes EMF lines to an io.Writer.
type Emitter struct {
	w   io.Writer
	now func() time.Time
}

// NewEmitter returns an Emitter writing to w.
func NewEmitter(w io.Writer) *Emitter {
	return &Emitter{w: w, now: time.Now}
}

// Default writes to stdout, where the ECS awslogs driver ships each line to
// CloudWatch Logs and CloudWatch extracts the embedded metric. Tests reassign it.
var Default = NewEmitter(os.Stdout)

type emfMetricDefinition struct {
	Name string `json:"Name"`
	Unit string `json:"Unit"`
}

type emfMetricDirective struct {
	Namespace  string                `json:"Namespace"`
	Dimensions [][]string            `json:"Dimensions"`
	Metrics    []emfMetricDefinition `json:"Metrics"`
}

type emfMetadata struct {
	Timestamp         int64                `json:"Timestamp"`
	CloudWatchMetrics []emfMetricDirective `json:"CloudWatchMetrics"`
}

// RateLimitRejection writes one EMF line recording a rate-limit rejection for
// endpoint. endpoint becomes the metric dimension value; ip rides along as a
// searchable property but is never a dimension (unbounded cardinality). It never
// returns an error and never blocks: emitting a metric must not affect the
// request that triggered it.
//
// DimensionEndpoint is used as both the declared dimension name and the property
// key, so the value CloudWatch reads always matches the dimension it declares.
func (e *Emitter) RateLimitRejection(endpoint, ip string) {
	payload := map[string]any{
		"_aws": emfMetadata{
			Timestamp: e.now().UnixMilli(),
			CloudWatchMetrics: []emfMetricDirective{{
				Namespace:  Namespace,
				Dimensions: [][]string{{DimensionEndpoint}},
				Metrics:    []emfMetricDefinition{{Name: MetricRateLimitRejections, Unit: "Count"}},
			}},
		},
		DimensionEndpoint:         endpoint,
		"ip":                      ip,
		MetricRateLimitRejections: 1,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return // unreachable for this fixed shape; never fail the caller
	}
	fmt.Fprintln(e.w, string(b))
}
