resource "aws_sqs_queue" "events_enriched_dlq" {
  name                      = "${var.app_name_prefix}-events-enriched-dlq"
  message_retention_seconds = 1209600 # 14 days
}

# Enriched events, read by the ECS ingest consumer. Same shape as the events
# queue: the consumer is an ordinary long-polling reader, not a Lambda trigger,
# so 30s visibility is right here.
resource "aws_sqs_queue" "events_enriched" {
  name                       = "${var.app_name_prefix}-events-enriched-queue"
  visibility_timeout_seconds = 30
  receive_wait_time_seconds  = 20
  message_retention_seconds  = 345600

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.events_enriched_dlq.arn
    maxReceiveCount     = 3
  })
}

resource "aws_cloudwatch_metric_alarm" "events_enriched_dlq_depth" {
  alarm_name          = "${var.app_name_prefix}-events-enriched-dlq-depth"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 1
  dimensions          = { QueueName = aws_sqs_queue.events_enriched_dlq.name }
  alarm_description   = "Messages landed in the enriched-events DLQ. Check consumer logs."
  alarm_actions       = [aws_sns_topic.alerts.arn]
}

# Skip cache for enrichment attempts. Its OWN bucket rather than a prefix in the
# posters bucket: iam.tf grants the ECS task role ${posters.arn}/*, so a prefix
# there would hand the API read/write on a cache it has no business touching.
resource "aws_s3_bucket" "enrichment_cache" {
  bucket = "${var.app_name_prefix}-enrichment-cache-${data.aws_caller_identity.current.account_id}"
  tags   = { App = var.app_name_prefix }
}

resource "aws_s3_bucket_public_access_block" "enrichment_cache" {
  bucket                  = aws_s3_bucket.enrichment_cache.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "enrichment_cache" {
  bucket = aws_s3_bucket.enrichment_cache.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# setlist.fm API key. NOTE: the free key is non-commercial only and capped at
# 1,440 requests/day; the upgrade path is a manual request to setlist.fm.
resource "aws_secretsmanager_secret" "setlistfm_key" {
  name                    = "${var.app_name_prefix}/setlistfm-api-key"
  description             = "setlist.fm API key for enrichment. Seeded out-of-band."
  recovery_window_in_days = 7
  tags                    = { App = var.app_name_prefix }
}

# Placeholder version; the real key is written post-apply via:
#   aws secretsmanager put-secret-value --secret-id hwh/setlistfm-api-key --secret-string "<key>"
resource "aws_secretsmanager_secret_version" "setlistfm_key_placeholder" {
  secret_id     = aws_secretsmanager_secret.setlistfm_key.id
  secret_string = "REPLACE_ME_AFTER_APPLY"
  lifecycle {
    ignore_changes = [secret_string]
  }
}

# enrichment-cache-{read,write}-failed alerting, plus the degrade paths added
# alongside the schema-validation fixes below.
#
# enrichment-cache.ts deliberately throws on a cache read AccessDenied so a
# misconfiguration is loud, but enrichment.ts (by design — a cache failure
# must not become an ingest outage) catches it and only console.errors. Left
# there, a wrong bucket or missing IAM is silent: every workflow re-runs for
# every event on every daily scrape (~200 events x ~5 LLM calls) with nothing
# but log lines to notice it by.
#
# The same silence applies to four MORE paths added when the cache and the
# outbound SQS message gained runtime schema validation: a cached object that
# fails validation is treated as a miss (split into a corrupt-JSON case and a
# schema-invalid case), a payload that fails validation is never written in
# the first place, and an invalid enriched message is emitted without its
# enrichment block — every one of those "degrades gracefully" outcomes is
# still a workflow silently re-running forever for one artist, or an artist
# section silently vanishing from the API, and needs the same alarm treatment
# as the read/write failures.
locals {
  enrichment_cache_alarms = {
    enrichment-cache-read-failed = {
      metric_name = "EnrichmentCacheReadFailed"
      description = "enrichment cache read failed (e.g. AccessDenied) — every workflow is now re-running for every event. Check IAM and ENRICHMENT_CACHE_BUCKET."
    }
    enrichment-cache-write-failed = {
      metric_name = "EnrichmentCacheWriteFailed"
      description = "enrichment cache write failed — successful enrichment is not being cached, so it re-runs every scrape. Check IAM and ENRICHMENT_CACHE_BUCKET."
    }
    enrichment-cache-corrupt-json = {
      metric_name = "EnrichmentCacheCorruptJson"
      description = "A cached enrichment object was not valid JSON and was treated as a miss, so its workflows are re-running every scrape. Check for S3 data corruption or a partial write."
    }
    enrichment-cache-invalid-entry = {
      metric_name = "EnrichmentCacheInvalidEntry"
      description = "A cached enrichment object failed schema validation on read and was treated as a miss, so its workflows are re-running every scrape. Check for a schema drift between writer and reader, or a hand-edited object."
    }
    enrichment-cache-invalid-write = {
      metric_name = "EnrichmentCacheInvalidWrite"
      description = "A workflow's result failed schema validation before being cached, so the write was skipped. Check the workflow that produced it — likely an unchecked cast from an external API response (Wikimedia, setlist.fm, MusicBrainz)."
    }
    enrichment-invalid-output = {
      metric_name = "EnrichmentInvalidOutput"
      description = "An enriched event failed schema validation before being emitted to the enriched queue; the plain event was emitted instead, so its enrichment silently never appears. Check the workflow that produced the invalid enrichment block."
    }
  }
}

resource "aws_cloudwatch_log_metric_filter" "enrichment_cache" {
  for_each = local.enrichment_cache_alarms

  name           = "${var.app_name_prefix}-${each.key}"
  log_group_name = "/aws/lambda/${aws_lambda_function.mastra_handler.function_name}"
  pattern        = "\"${each.key}\""

  metric_transformation {
    name      = each.value.metric_name
    namespace = "HeresWhatsHappening/enrichment"
    value     = "1"
  }
}

resource "aws_cloudwatch_metric_alarm" "enrichment_cache" {
  for_each = local.enrichment_cache_alarms

  alarm_name          = "${var.app_name_prefix}-${each.key}"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = each.value.metric_name
  namespace           = "HeresWhatsHappening/enrichment"
  period              = 300
  statistic           = "Sum"
  threshold           = 1
  treat_missing_data  = "notBreaching" # sparse metric: a data point only when a failure occurs
  alarm_actions       = [aws_sns_topic.alerts.arn]
  alarm_description   = each.value.description

  depends_on = [aws_cloudwatch_log_metric_filter.enrichment_cache]
}

# Enrichment trigger. Added in a SEPARATE apply, after an image that handles SQS
# events is live: with this in place, the first scrape invokes the function with
# an SQS event, and an older image would fall through to its S3 branch.
resource "aws_lambda_event_source_mapping" "enrichment" {
  event_source_arn = aws_sqs_queue.events.arn
  function_name    = aws_lambda_function.mastra_handler.arn

  # One event per invocation. On the merits: one event is three workflows with
  # LLM calls, and batching ten into a 300s timeout blows the budget.
  # Structurally: it avoids partial-batch-failure reporting, which SQS reads
  # from a JSON response body — not something to depend on through the
  # streamifyResponse wrapper. At 1, success deletes and a throw returns.
  batch_size = 1

  # Bounds the SQS path ONLY. reserved_concurrent_executions would be wrong:
  # this function also serves the poster Function URL and the email S3 path, and
  # reserving would starve interactive poster requests behind enrichment.
  # 2 is the minimum AWS allows.
  scaling_config {
    maximum_concurrency = 2
  }
}
