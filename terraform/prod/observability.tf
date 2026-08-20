# Rate-limit rejection alerting.
#
# The app emits a CloudWatch EMF metric on each 429 (see internal/observability):
# namespace "HeresWhatsHappening/api", metric "RateLimitRejections", dimension
# "endpoint". The app defines eleven endpoint values in
# internal/http/middleware/ratelimit.go; the subset alarmed below MUST match
# those constants exactly — TestMetricContractConstants (internal/observability)
# guards the namespace/metric-name/dimension-key contract, while
# TestEndpointConstants (internal/http/middleware/ratelimit_test.go) pins the
# endpoint values the alarm map keys mirror.
# Values not listed here are still emitted and queryable in Logs Insights, they
# just do not page. The metric is sparse (a data point only when a rejection
# occurs), so the alarms treat missing data as not breaching.

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

    authed           = { threshold = 50, description = "Sustained authenticated rate-limit rejections — an account is being scripted or a client is looping." }
    manual_interests = { threshold = 10, description = "Manual-interest rejections — each allowed call publishes to the interests queue, so this caps runaway downstream compute." }
    spotify_exchange = { threshold = 5, description = "Spotify OAuth exchange rejections — this quota is shared across all users, so abuse here breaks the integration for everyone." }
    poster_create    = { threshold = 5, description = "Poster-creation rejections — each allowed call can drive nine LLM requests, so this caps runaway spend from one account." }
    ical_feed        = { threshold = 100, description = "Sustained iCal feed rejections — an unauthenticated route being flooded from one source." }
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

# Database connection-pool alerting.
#
# internal/db's sampler emits one EMF line per minute per task
# (internal/observability.PoolStats): namespace "HeresWhatsHappening/api",
# dimension "service" with values "api", "match" and "scrape-spotify".
# TestPoolMetricContractConstants (internal/observability) pins the metric names
# and the dimension key these alarms mirror.
#
# Only "api" is alarmed. It is the always-on, user-facing task, and it shares
# ten connections between the HTTP handlers and eight ingest worker goroutines
# (two consumers x var.ingest_workers), so it is where pool pressure shows up
# first. The scheduled families emit the same metrics and stay queryable in Logs
# Insights; they just do not page.
#
# Unlike RateLimitRejections these metrics are continuous, not sparse — a gap
# means the task is not running, which a deploy produces routinely, so missing
# data is treated as not breaching.
locals {
  db_pool_alarms = {
    pool-canceled-acquires = {
      metric_name = "DBPoolCanceledAcquires"
      statistic   = "Sum"
      threshold   = 3
      periods     = 1
      comparison  = "GreaterThanOrEqualToThreshold"
      description = "Requests gave up waiting for a database connection — their context deadline fired before the pool had one free. User-visible failures; the pool is too small or something is holding connections too long."
    }
    pool-wait-time = {
      metric_name = "DBPoolWaitMillis"
      statistic   = "Sum"
      threshold   = 5000
      periods     = 2
      comparison  = "GreaterThanOrEqualToThreshold"
      description = "Sustained time spent blocked on an empty connection pool (5s cumulative per 5-minute period). Rising wait time is the earliest signal that the pool is undersized or a query is holding connections too long."
    }
    pool-saturated = {
      metric_name = "DBPoolInUseConns"
      statistic   = "Maximum"
      threshold   = 9
      periods     = 3
      comparison  = "GreaterThanOrEqualToThreshold"
      description = "The api pool has been at or near its ten-connection ceiling for fifteen minutes. Expect queueing next; raise MaxConns in internal/db or shed load."
    }
  }
}

resource "aws_cloudwatch_metric_alarm" "db_pool" {
  for_each = local.db_pool_alarms

  alarm_name          = "${var.app_name_prefix}-${each.key}"
  comparison_operator = each.value.comparison
  evaluation_periods  = each.value.periods
  metric_name         = each.value.metric_name
  namespace           = "HeresWhatsHappening/api"
  period              = 300
  statistic           = each.value.statistic
  threshold           = each.value.threshold
  dimensions          = { service = "api" }
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
  alarm_description   = each.value.description
}
