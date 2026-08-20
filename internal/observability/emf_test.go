package observability_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/observability"
)

func TestRateLimitRejection_EmitsContractCompliantEMF(t *testing.T) {
	var buf bytes.Buffer
	observability.NewEmitter(&buf).RateLimitRejection("signup", "203.0.113.9")

	out := buf.Bytes()
	require.Equal(t, 1, bytes.Count(out, []byte("\n")), "exactly one line")
	trimmed := bytes.TrimSpace(out)
	require.True(t, json.Valid(trimmed), "EMF line must be valid JSON with no log prefix")

	var m map[string]any
	require.NoError(t, json.Unmarshal(trimmed, &m))

	// Top-level metric value and properties.
	require.EqualValues(t, 1, m["RateLimitRejections"])
	require.Equal(t, "signup", m["endpoint"], "endpoint is the dimension value")
	require.Equal(t, "203.0.113.9", m["ip"], "ip is a searchable property")

	// The _aws directive the CloudWatch alarms depend on.
	aws := m["_aws"].(map[string]any)
	require.NotZero(t, aws["Timestamp"], "EMF requires an epoch-millis timestamp")
	directive := aws["CloudWatchMetrics"].([]any)[0].(map[string]any)
	require.Equal(t, "HeresWhatsHappening/api", directive["Namespace"])
	require.Equal(t, []any{[]any{"endpoint"}}, directive["Dimensions"])
	metricDef := directive["Metrics"].([]any)[0].(map[string]any)
	require.Equal(t, "RateLimitRejections", metricDef["Name"])
	require.Equal(t, "Count", metricDef["Unit"])
}

// The property key that carries the dimension value must equal the declared
// dimension name, or CloudWatch cannot resolve the dimension. Using the same
// constant for both (below) guarantees it; this test pins it.
func TestRateLimitRejection_DimensionKeyMatchesDeclaredDimension(t *testing.T) {
	var buf bytes.Buffer
	observability.NewEmitter(&buf).RateLimitRejection("login", "1.1.1.1")

	var m map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m))
	directive := m["_aws"].(map[string]any)["CloudWatchMetrics"].([]any)[0].(map[string]any)
	dimName := directive["Dimensions"].([]any)[0].([]any)[0].(string)
	_, present := m[dimName]
	require.True(t, present, "the declared dimension %q must exist as a top-level property", dimName)
}

// These four strings are duplicated in terraform/prod/observability.tf. If you
// change one, update the alarms there or they go blind.
func TestMetricContractConstants(t *testing.T) {
	require.Equal(t, "HeresWhatsHappening/api", observability.Namespace)
	require.Equal(t, "RateLimitRejections", observability.MetricRateLimitRejections)
	require.Equal(t, "endpoint", observability.DimensionEndpoint)
}

// The pool-stats line is what makes WaitCount/WaitDuration visible in
// CloudWatch. It carries counter DELTAS (per interval) alongside instantaneous
// gauges, so a CloudWatch Sum over the counters is meaningful — summing the
// monotonic since-pool-creation totals pgxpool reports would not be.
func TestPoolStats_EmitsContractCompliantEMF(t *testing.T) {
	var buf bytes.Buffer
	observability.NewEmitter(&buf).PoolStats("api", observability.PoolSample{
		InUseConns: 8, IdleConns: 2, TotalConns: 10, MaxConns: 10,
		Acquires: 140, Waits: 12, CanceledAcquires: 3,
		WaitDuration: 250 * time.Millisecond,
	})

	out := buf.Bytes()
	require.Equal(t, 1, bytes.Count(out, []byte("\n")), "exactly one line")
	trimmed := bytes.TrimSpace(out)
	require.True(t, json.Valid(trimmed), "EMF line must be valid JSON with no log prefix")

	var m map[string]any
	require.NoError(t, json.Unmarshal(trimmed, &m))

	require.Equal(t, "api", m["service"], "service is the dimension value")
	require.EqualValues(t, 8, m["DBPoolInUseConns"])
	require.EqualValues(t, 2, m["DBPoolIdleConns"])
	require.EqualValues(t, 10, m["DBPoolTotalConns"])
	require.EqualValues(t, 10, m["DBPoolMaxConns"])
	require.EqualValues(t, 140, m["DBPoolAcquires"])
	require.EqualValues(t, 12, m["DBPoolWaits"])
	require.EqualValues(t, 3, m["DBPoolCanceledAcquires"])
	require.EqualValues(t, 250, m["DBPoolWaitMillis"], "duration is emitted in whole milliseconds")

	aws := m["_aws"].(map[string]any)
	require.NotZero(t, aws["Timestamp"])
	directive := aws["CloudWatchMetrics"].([]any)[0].(map[string]any)
	require.Equal(t, "HeresWhatsHappening/api", directive["Namespace"])
	require.Equal(t, []any{[]any{"service"}}, directive["Dimensions"])

	units := map[string]string{}
	for _, d := range directive["Metrics"].([]any) {
		md := d.(map[string]any)
		units[md["Name"].(string)] = md["Unit"].(string)
	}
	require.Equal(t, "Milliseconds", units["DBPoolWaitMillis"], "wait time must not be typed as a Count")
	require.Equal(t, "Count", units["DBPoolWaits"])
	require.Len(t, units, 8, "every emitted metric must be declared in the directive")
}

// These strings are duplicated in terraform/prod/observability.tf. If you
// change one, update the alarms there or they go blind.
func TestPoolMetricContractConstants(t *testing.T) {
	require.Equal(t, "service", observability.DimensionService)
	require.Equal(t, "DBPoolInUseConns", observability.MetricPoolInUseConns)
	require.Equal(t, "DBPoolIdleConns", observability.MetricPoolIdleConns)
	require.Equal(t, "DBPoolTotalConns", observability.MetricPoolTotalConns)
	require.Equal(t, "DBPoolMaxConns", observability.MetricPoolMaxConns)
	require.Equal(t, "DBPoolAcquires", observability.MetricPoolAcquires)
	require.Equal(t, "DBPoolWaits", observability.MetricPoolWaits)
	require.Equal(t, "DBPoolCanceledAcquires", observability.MetricPoolCanceledAcquires)
	require.Equal(t, "DBPoolWaitMillis", observability.MetricPoolWaitMillis)
}
