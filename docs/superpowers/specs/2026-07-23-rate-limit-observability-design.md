# Rate-Limit Rejection Observability

**Date:** 2026-07-23
**Status:** Approved, ready for implementation planning
**Depends on:** the API rate limiting feature (`2026-07-22-api-rate-limiting-design.md` /
branch `feature/api-rate-limiting`, PR #24). This work modifies `checkAllowed`, which that
feature introduces, and is built as a stacked branch (`feature/rate-limit-observability`) on
top of it.

## Problem

When the rate limiter rejects a request with a 429, nothing records it. Under normal usage a
rejection should essentially never happen — a real user hitting the signup, login, or refresh
limit is either a bug, a misconfiguration (e.g. `TRUST_PROXY` not live, collapsing every caller
into one bucket), or abuse. We want an email whenever rejections occur, with signup treated
strictly (any rejection is notable) and login/refresh treated tolerantly (page only on sustained
volume).

## Approach

Emit a CloudWatch **Embedded Metric Format (EMF)** log line from the app on each rejection.
CloudWatch extracts a custom metric from it on ingestion; per-endpoint alarms notify a new SNS
topic that emails the operator. No new queue, no new runtime dependency in the request path, and
no fragile log-pattern string in Terraform — the app owns the metric definition.

### Why not the alternatives

- **A dedicated SQS queue + queue-depth alarm** (the original idea) is the heaviest option and
  has awkward semantics: `ApproximateNumberOfMessagesVisible > 0` only holds while messages
  *accumulate* (nothing consuming them), so the alarm reflects unconsumed backlog rather than
  "an event happened," and resetting it to OK means manually purging the queue. It also puts a
  publish call in the request path, which — done fire-and-forget to avoid blocking the response —
  can silently drop the very event being observed.
- **A CloudWatch Logs metric filter on a plain log line** works and needs no EMF, but the filter
  pattern is a string contract in Terraform that silently goes quiet if the log line is edited,
  and per-endpoint dimensions are clumsier to express. It is retained as the documented fallback
  (see "Plan B" below) in case EMF auto-extraction misbehaves over the ECS `awslogs` path.

## Existing infrastructure this builds on

- The API already streams stdout to CloudWatch Logs group `/aws/ecs/<prefix>/api` via the
  `awslogs` driver (`terraform/prod/cloudwatch.tf`), so the EMF line reaches CloudWatch with no
  new transport.
- `terraform/prod/sqs.tf` already uses `aws_cloudwatch_metric_alarm` for DLQ depth — the alarm
  pattern to mirror. **Those alarms currently have no `alarm_actions`, so they notify no one**;
  this work wires them to the new topic as a cleanup.
- `terraform/bootstrap/sns.tf` establishes the SNS-topic + confirmed-email-subscription pattern
  (`approval_email` → wlmmyers@gmail.com). Prod is a separate Terraform state, so this adds a
  prod-side topic and `alert_email` variable.
- All three rejections already flow through one function, `checkAllowed`
  (`internal/http/middleware/ratelimit.go`), which knows the endpoint via its `name` argument
  (`"signup"` / `"login"` / `"refresh"` from the route wiring). One emit call there covers all
  three.

## Application changes

### New package `internal/observability`

A dependency-free EMF emitter.

```go
package observability

// Emitter writes CloudWatch Embedded Metric Format lines to an io.Writer.
type Emitter struct{ w io.Writer }

func NewEmitter(w io.Writer) *Emitter

// RateLimitRejection records one rate-limit rejection for endpoint. It writes a
// single EMF JSON line: endpoint is the metric DIMENSION (low cardinality — three
// values), ip is a plain property so the detail is searchable in the log without
// creating a metric stream per IP.
func (e *Emitter) RateLimitRejection(endpoint, ip string)

// Default emits to os.Stdout. Tests override it to capture output.
var Default = NewEmitter(os.Stdout)
```

The emitted line, exactly (no log prefix — see below):

```json
{"_aws":{"Timestamp":1690000000000,"CloudWatchMetrics":[{"Namespace":"HeresWhatsHappening/api","Dimensions":[["endpoint"]],"Metrics":[{"Name":"RateLimitRejections","Unit":"Count"}]}]},"endpoint":"signup","ip":"203.0.113.9","RateLimitRejections":1}
```

- `_aws.Timestamp` is epoch **milliseconds** (`time.Now().UnixMilli()`).
- `RateLimitRejections` = 1 per rejection; alarms aggregate with `SUM` over the period.
- The metric is defined by the JSON marshaled from typed structs, not by string concatenation.

**Critical: EMF lines must not carry a log prefix.** The standard `log.Printf` prepends a
timestamp that would make the line invalid JSON and break extraction. The emitter writes the raw
JSON object followed by a newline (`fmt.Fprintln(e.w, string(jsonBytes))`), a path distinct from
the existing `log.Printf` error logging in the middleware, which is unchanged.

### Call site

In `checkAllowed` (`internal/http/middleware/ratelimit.go`), immediately before writing the 429:

```go
observability.Default.RateLimitRejection(name, ClientIP(r))
```

`name` is already the endpoint; `ClientIP(r)` is the resolved client IP (Task 1 of the rate-limit
feature). No signature changes, so `server.go` wiring is untouched.

### The app↔alarm contract

The alarms reference the metric by `Namespace = "HeresWhatsHappening/api"`, `MetricName =
"RateLimitRejections"`, dimension name `endpoint`, and dimension values `signup` / `login` /
`refresh`. These four things are the coupling between app and infrastructure (the EMF equivalent
of a metric-filter's pattern string). To lock the app side, a Go test asserts the emitted JSON
contains exactly that namespace, metric name, dimension name, and endpoint value. A comment at
the Terraform alarms points back to this constant. The namespace/metric name live as exported
constants in `internal/observability` so there is a single source in the app.

## Infrastructure changes (`terraform/prod`)

New file `terraform/prod/observability.tf`:

- `aws_sns_topic` `<prefix>-alerts`.
- `aws_sns_topic_subscription` email → `var.alert_email`. **Requires a one-time manual
  confirmation** via the link AWS emails after apply (same as the bootstrap infra-approval
  subscription).
- New variable `alert_email` in `variables.tf`, set in the prod tfvars to `wlmmyers@gmail.com`.
- Three `aws_cloudwatch_metric_alarm`, one per endpoint dimension, all with
  `alarm_actions = [aws_sns_topic.alerts.arn]`, `namespace = "HeresWhatsHappening/api"`,
  `metric_name = "RateLimitRejections"`, `statistic = "Sum"`, `period = 300`,
  `treat_missing_data = "notBreaching"` (the metric is sparse — data points appear only when a
  rejection occurs — so missing data is the healthy state):

  | Alarm | Dimension `endpoint` | Threshold | Rationale |
  |---|---|---|---|
  | `<prefix>-ratelimit-signup` | `signup` | `Sum >= 1` / 5 min | A real user hitting 3/hour is rare; any rejection is a bot or a bug. |
  | `<prefix>-ratelimit-login` | `login` | `Sum >= 20` / 5 min | A flaky client can trip 10/min benignly; alert on sustained/broad abuse. |
  | `<prefix>-ratelimit-refresh` | `refresh` | `Sum >= 50` / 5 min | Highest limit (30/min), most benign trips; loosest threshold. |

  The login/refresh thresholds are tuning knobs; these are conservative starting values.

- **DLQ alarm cleanup:** add `alarm_actions = [aws_sns_topic.alerts.arn]` (and
  `ok_actions` optional) to the two existing DLQ-depth alarms in `sqs.tf` so they finally notify.

## Failure modes and notes

- **Emission never affects the response.** `RateLimitRejection` writes one line to stdout and
  returns; it has no error path that the handler observes. A write failure to stdout is not
  something the request should care about.
- **Alarms start in `INSUFFICIENT_DATA`** until the first rejection ever occurs, then oscillate
  between OK (missing = notBreaching) and ALARM. This is expected and correct for a "quiet until
  it happens" signal.
- **TRUST_PROXY interaction:** if `TRUST_PROXY` is not live, every request keys to the ALB IP and
  rejections would spike across all three endpoints — this alarm set would fire loudly, which is
  a useful early warning of that misconfiguration rather than a nuisance.
- **Cost is negligible:** three custom metrics (~$0.30/metric/month) plus standard log ingestion
  already being paid.

## Plan B (documented fallback, not the primary plan)

If EMF auto-extraction proves unreliable over the ECS `awslogs` ingestion path, fall back to a
`aws_cloudwatch_log_metric_filter` with a dimension: the app emits the same structured line, and
the filter (defined in Terraform, with `default_value = 0`) extracts the per-endpoint metric. The
alarms and SNS wiring are identical. This trades EMF's app-owned definition for a Terraform-owned
pattern string, and is only adopted if a smoke test shows EMF extraction failing.

## Out of scope

- Dashboards, tracing, or per-IP metrics (IP stays a log property, never a dimension).
- Alerting on anything other than rate-limit rejections and the two existing DLQs.
- Auto-remediation. This is notification only.
