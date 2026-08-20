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

// Pool metric contract. Like the rate-limit constants above, these are
// referenced verbatim by the alarms in terraform/prod/observability.tf;
// TestPoolMetricContractConstants guards them.
const (
	DimensionService = "service"

	MetricPoolInUseConns       = "DBPoolInUseConns"
	MetricPoolIdleConns        = "DBPoolIdleConns"
	MetricPoolTotalConns       = "DBPoolTotalConns"
	MetricPoolMaxConns         = "DBPoolMaxConns"
	MetricPoolAcquires         = "DBPoolAcquires"
	MetricPoolWaits            = "DBPoolWaits"
	MetricPoolCanceledAcquires = "DBPoolCanceledAcquires"
	MetricPoolWaitMillis       = "DBPoolWaitMillis"
)

// PoolSample is one interval's worth of connection-pool statistics.
//
// The connection counts are instantaneous gauges. The rest are DELTAS over the
// sampling interval, not the monotonic since-pool-creation totals pgxpool
// reports: a CloudWatch Sum or Average over a monotonic counter is meaningless,
// so the sampler differences them before they reach here.
type PoolSample struct {
	// Gauges — utilisation at the moment of sampling.
	InUseConns int32
	IdleConns  int32
	TotalConns int32
	MaxConns   int32

	// Deltas over the interval.
	Acquires int64 // total acquires
	// Waits is the count of acquires that had to block because the pool was
	// empty — the signal that the pool is too small, or that something is
	// holding connections too long.
	Waits int64
	// CanceledAcquires counts acquires abandoned because the caller's context
	// expired while it waited. Non-zero means a query deadline fired before a
	// connection ever became available.
	CanceledAcquires int64
	// WaitDuration is time spent blocked on an empty pool. Rising wait time is
	// the thing to alarm on.
	WaitDuration time.Duration
}

// PoolStats writes one EMF line of pool statistics for service ("api",
// "match", ...), which becomes the metric dimension so the task families stay
// distinguishable. Like RateLimitRejection it never fails the caller.
func (e *Emitter) PoolStats(service string, s PoolSample) {
	payload := map[string]any{
		"_aws": emfMetadata{
			Timestamp: e.now().UnixMilli(),
			CloudWatchMetrics: []emfMetricDirective{{
				Namespace:  Namespace,
				Dimensions: [][]string{{DimensionService}},
				Metrics: []emfMetricDefinition{
					{Name: MetricPoolInUseConns, Unit: "Count"},
					{Name: MetricPoolIdleConns, Unit: "Count"},
					{Name: MetricPoolTotalConns, Unit: "Count"},
					{Name: MetricPoolMaxConns, Unit: "Count"},
					{Name: MetricPoolAcquires, Unit: "Count"},
					{Name: MetricPoolWaits, Unit: "Count"},
					{Name: MetricPoolCanceledAcquires, Unit: "Count"},
					{Name: MetricPoolWaitMillis, Unit: "Milliseconds"},
				},
			}},
		},
		DimensionService:           service,
		MetricPoolInUseConns:       s.InUseConns,
		MetricPoolIdleConns:        s.IdleConns,
		MetricPoolTotalConns:       s.TotalConns,
		MetricPoolMaxConns:         s.MaxConns,
		MetricPoolAcquires:         s.Acquires,
		MetricPoolWaits:            s.Waits,
		MetricPoolCanceledAcquires: s.CanceledAcquires,
		MetricPoolWaitMillis:       s.WaitDuration.Milliseconds(),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return // unreachable for this fixed shape; never fail the caller
	}
	fmt.Fprintln(e.w, string(b))
}
