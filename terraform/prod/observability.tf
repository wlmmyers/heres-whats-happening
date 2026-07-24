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
