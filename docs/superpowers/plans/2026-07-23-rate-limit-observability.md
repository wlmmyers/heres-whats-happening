# Rate-Limit Rejection Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Emit a CloudWatch EMF metric on every rate-limit 429 (dimensioned by endpoint) and alarm per-endpoint to a new SNS topic that emails the operator, plus wire the existing unwired DLQ alarms to the same topic.

**Architecture:** A dependency-free EMF emitter in `internal/observability` writes one structured JSON line per rejection to stdout; the ECS `awslogs` driver ships it to CloudWatch Logs, which extracts a custom metric. `checkAllowed` in the rate-limit middleware calls the emitter. Terraform in `terraform/prod` defines the SNS topic, email subscription, three per-endpoint alarms, and the DLQ-alarm wiring.

**Tech Stack:** Go 1.26 (stdlib `encoding/json` only), chi middleware, Terraform (AWS provider), CloudWatch EMF + metric alarms + SNS.

**Spec:** `docs/superpowers/specs/2026-07-23-rate-limit-observability-design.md`

**Branch:** `feature/rate-limit-observability`, stacked on `feature/api-rate-limiting` (PR #24). It modifies `checkAllowed`, which only exists on that branch.

## Global Constraints

- **The metric contract is four exact strings** shared between the app and the alarms: namespace `HeresWhatsHappening/api`, metric name `RateLimitRejections`, dimension name `endpoint`, dimension values `signup` / `login` / `refresh`. The app defines them as constants; the Terraform alarms must match verbatim.
- **An EMF line must be exactly the JSON object** — no `log` timestamp prefix, or CloudWatch cannot parse it. Write raw JSON + newline.
- **Emitting a metric must never affect the HTTP response.** The emitter returns nothing and has no error the handler observes.
- `ip` is a plain log property, **never a metric dimension** (unbounded cardinality). Only `endpoint` is a dimension.
- Alarm thresholds, exactly: signup `Sum >= 1`, login `Sum >= 20`, refresh `Sum >= 50`, each over `period = 300`, `statistic = "Sum"`, `treat_missing_data = "notBreaching"`.
- Alerts email `wlmmyers@gmail.com` via a new `alert_email` prod variable.
- **No new Go module dependencies.**
- **Do not run `terraform apply`, `terraform plan` against real state, or any AWS/deploy command.** `terraform/prod` applies via CodeBuild on merge; rollout and the one-time email-subscription confirmation are the repo owner's.
- Commit on `feature/rate-limit-observability`. No branching, rebasing, pushing, or PRs unless asked.

## File Structure

| File | Responsibility |
|---|---|
| `internal/observability/emf.go` (create) | `Emitter`, `Default`, contract constants, `RateLimitRejection` |
| `internal/observability/emf_test.go` (create) | EMF shape + contract-constant lock |
| `internal/http/middleware/ratelimit.go` (modify) | Call the emitter in `checkAllowed` |
| `internal/http/middleware/ratelimit_test.go` (modify) | Emits on 429, silent when allowed |
| `terraform/prod/observability.tf` (create) | SNS topic, email subscription, 3 alarms |
| `terraform/prod/variables.tf` (modify) | `alert_email` variable |
| `terraform/prod/prod.auto.tfvars`, `terraform.tfvars` (modify) | Set `alert_email` |
| `terraform/prod/sqs.tf` (modify) | Add `alarm_actions` to the two DLQ alarms |

---

### Task 1: EMF emitter package

**Files:**
- Create: `internal/observability/emf.go`
- Create: `internal/observability/emf_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `observability.Namespace`, `observability.MetricRateLimitRejections`, `observability.DimensionEndpoint` (string consts)
  - `observability.NewEmitter(w io.Writer) *Emitter`
  - `observability.Default *Emitter`
  - `(*Emitter).RateLimitRejection(endpoint, ip string)`

Task 2 calls `observability.Default.RateLimitRejection`.

- [ ] **Step 1: Write the failing tests**

Create `internal/observability/emf_test.go`:

```go
package observability_test

import (
	"bytes"
	"encoding/json"
	"testing"

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
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/observability/ -v
```

Expected: build failure — package does not exist.

- [ ] **Step 3: Implement the emitter**

Create `internal/observability/emf.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/observability/ -v
```

Expected: all three PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/observability/emf.go internal/observability/emf_test.go
git commit -m "feat: add EMF emitter for rate-limit rejection metrics"
```

---

### Task 2: Emit on rejection from the middleware

**Files:**
- Modify: `internal/http/middleware/ratelimit.go:63-76` (`checkAllowed`)
- Modify: `internal/http/middleware/ratelimit_test.go`

**Interfaces:**
- Consumes: `observability.Default.RateLimitRejection` (Task 1); `middleware.ClientIP` (existing).
- Produces: nothing downstream.

- [ ] **Step 1: Write the failing tests**

Add to `internal/http/middleware/ratelimit_test.go`. Add `"bytes"` and `"github.com/wmyers/heres-whats-happening/internal/observability"` to its imports.

```go
func TestRateLimit_EmitsMetricOnRejection(t *testing.T) {
	var buf bytes.Buffer
	old := observability.Default
	observability.Default = observability.NewEmitter(&buf)
	defer func() { observability.Default = old }()

	l := &stubLimiter{decision: ratelimit.Decision{Allowed: false, RetryAfter: time.Minute}}
	h := middleware.RateLimit(l, "login")(okHandler(http.StatusOK))
	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	require.Contains(t, out, `"endpoint":"login"`, "metric must carry the endpoint dimension")
	require.Contains(t, out, `"ip":"203.0.113.9"`, "metric must carry the client ip property")
	require.Contains(t, out, "RateLimitRejections")
}

func TestRateLimit_NoMetricWhenAllowed(t *testing.T) {
	var buf bytes.Buffer
	old := observability.Default
	observability.Default = observability.NewEmitter(&buf)
	defer func() { observability.Default = old }()

	l := &stubLimiter{decision: ratelimit.Decision{Allowed: true}}
	h := middleware.RateLimit(l, "login")(okHandler(http.StatusOK))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	require.Empty(t, buf.String(), "an allowed request must emit no rejection metric")
}
```

These tests reassign the package global `observability.Default`, so they must not run in parallel — do not add `t.Parallel()` to them.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/http/middleware/ -run 'TestRateLimit_(EmitsMetricOnRejection|NoMetricWhenAllowed)' -v
```

Expected: the test file compiles (it references `observability`), and `TestRateLimit_EmitsMetricOnRejection` FAILS its `require.Contains` — nothing writes to the buffer yet because `checkAllowed` does not emit until Step 3. `TestRateLimit_NoMetricWhenAllowed` passes trivially (buffer empty, as expected).

- [ ] **Step 3: Wire the emit call**

In `internal/http/middleware/ratelimit.go`, add the import:

```go
	"github.com/wmyers/heres-whats-happening/internal/observability"
```

In `checkAllowed`, emit immediately after the allow check and before writing the 429:

```go
	if d.Allowed {
		return true
	}
	observability.Default.RateLimitRejection(name, ClientIP(r))
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(d.RetryAfter)))
	httperr.Write(w, http.StatusTooManyRequests, "rate_limited",
		"too many requests, please try again later")
	return false
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/http/middleware/ -v
```

Expected: all PASS, including the pre-existing rate-limit and client-IP tests. (The other tests that produce a 429 now emit an EMF line to real stdout — harmless test-log noise, not a failure.)

- [ ] **Step 5: Full build and suite**

```bash
go build ./... && make test
```

Expected: PASS. Postgres must be up for `make test` (`make db-up` / `make migrate` / `make migrate-test` if not).

- [ ] **Step 6: Commit**

```bash
git add internal/http/middleware/ratelimit.go internal/http/middleware/ratelimit_test.go
git commit -m "feat: emit rate-limit rejection metric on 429"
```

---

### Task 3: CloudWatch alarms, SNS topic, and DLQ-alarm wiring

**Files:**
- Create: `terraform/prod/observability.tf`
- Modify: `terraform/prod/variables.tf`
- Modify: `terraform/prod/prod.auto.tfvars`
- Modify: `terraform/prod/terraform.tfvars`
- Modify: `terraform/prod/sqs.tf` (the two existing DLQ alarms)

**Interfaces:**
- Consumes: the metric emitted by Tasks 1-2 (`HeresWhatsHappening/api` / `RateLimitRejections` / `endpoint`); `var.app_name_prefix` (existing, default `hwh`); `aws_sqs_queue.events_dlq`, `aws_sqs_queue.interests_dlq` (existing).
- Produces: `aws_sns_topic.alerts` (referenced by the DLQ alarm edits).

- [ ] **Step 1: Add the `alert_email` variable**

In `terraform/prod/variables.tf`, following the existing block style:

```hcl
variable "alert_email" {
  description = "Email address that receives operational alerts (rate-limit rejections, DLQ depth). The SNS subscription must be confirmed via the link AWS emails after apply."
  type        = string
}
```

- [ ] **Step 2: Set it in both tfvars files**

Both `terraform/prod/prod.auto.tfvars` and `terraform/prod/terraform.tfvars` are auto-loaded and currently carry the same keys (`domain_name`, `aws_region`, `app_name_prefix`). Add the same line to **both**, near `app_name_prefix`:

```hcl
alert_email = "wlmmyers@gmail.com"
```

- [ ] **Step 3: Create the alarms and topic**

Create `terraform/prod/observability.tf`:

```hcl
# Rate-limit rejection alerting.
#
# The app emits a CloudWatch EMF metric on each 429 (see internal/observability):
# namespace "HeresWhatsHappening/api", metric "RateLimitRejections", dimension
# "endpoint" with values signup/login/refresh. These four strings MUST match the
# constants in internal/observability/emf.go — TestMetricContractConstants guards
# the app side. The metric is sparse (a data point only when a rejection occurs),
# so the alarms treat missing data as not breaching.

resource "aws_sns_topic" "alerts" {
  name = "${var.app_name_prefix}-alerts"
}

resource "aws_sns_topic_subscription" "alerts_email" {
  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "email"
  endpoint  = var.alert_email
}

locals {
  # threshold = Sum of rejections over one 5-minute period that trips the alarm.
  # Signup is strict (any rejection is a bot or a bug); login/refresh are tolerant
  # (a flaky client can trip them benignly), so they page only on sustained volume.
  ratelimit_alarms = {
    signup  = { threshold = 1, description = "Signup rate-limit rejections — a real user hitting 3/hour is rare; likely a bot or a bug." }
    login   = { threshold = 20, description = "Sustained login rate-limit rejections — possible credential stuffing." }
    refresh = { threshold = 50, description = "Sustained refresh rate-limit rejections." }
  }
}

resource "aws_cloudwatch_metric_alarm" "ratelimit" {
  for_each = local.ratelimit_alarms

  alarm_name          = "${var.app_name_prefix}-ratelimit-${each.key}"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "RateLimitRejections"
  namespace           = "HeresWhatsHappening/api"
  period              = 300
  statistic           = "Sum"
  threshold           = each.value.threshold
  dimensions          = { endpoint = each.key }
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
  alarm_description   = each.value.description
}
```

- [ ] **Step 4: Wire the existing DLQ alarms to the topic**

In `terraform/prod/sqs.tf`, add one line to **each** of the two existing alarm resources (`aws_cloudwatch_metric_alarm.events_dlq_depth` and `aws_cloudwatch_metric_alarm.interests_dlq_depth`), after their `alarm_description`:

```hcl
  alarm_actions     = [aws_sns_topic.alerts.arn]
```

They currently have no `alarm_actions`, so this is the fix that makes them actually notify.

- [ ] **Step 5: Format and validate**

```bash
terraform -chdir=terraform/prod fmt
terraform -chdir=terraform/prod init -backend=false
terraform -chdir=terraform/prod validate
```

Expected: `fmt` leaves the files unchanged (or restyles yours), `validate` prints "Success! The configuration is valid."

`init -backend=false` installs the AWS provider without touching remote state or needing AWS credentials. If this environment has no network access to the provider registry, skip `init`/`validate`, run only `terraform -chdir=terraform/prod fmt`, and note in the report that validate could not run — the CodeBuild pipeline validates on merge. **Do not** run `terraform plan` or `apply`.

- [ ] **Step 6: Commit**

```bash
git add terraform/prod/observability.tf terraform/prod/variables.tf \
        terraform/prod/prod.auto.tfvars terraform/prod/terraform.tfvars terraform/prod/sqs.tf
git commit -m "feat: alarm on rate-limit rejections and wire DLQ alarms to email"
```

---

## Verification

After Task 3:

```bash
go build ./... && make test
terraform -chdir=terraform/prod fmt -check && terraform -chdir=terraform/prod validate
```

The end-to-end signal path cannot be exercised locally (it needs CloudWatch ingestion). It is verified in two halves: the app half by `TestRateLimitRejection_*` and the middleware emit tests (the exact bytes emitted), and the infra half by `terraform validate` plus the contract-constant test tying the alarm strings to the app constants. The live smoke test — confirm the metric appears in CloudWatch and the alarm emails — happens after deploy and the one-time email-subscription confirmation, and is the repo owner's step.

## Post-merge / deploy notes (for the repo owner, not this plan)

- The email subscription created in Task 3 sends a confirmation link on first apply; it must be clicked before any alarm can email.
- This branch is stacked on PR #24 (rate limiting). Merge order: #24 first, then this. If #24 changes in review, rebase this branch onto it.
- EMF auto-extraction over the ECS `awslogs` path is the one assumption to confirm with the post-deploy smoke test. If the metric does not appear, the spec's documented Plan B (a `aws_cloudwatch_log_metric_filter` with an `endpoint` dimension over the same log line) is the fallback; the alarms and SNS wiring stay identical.
