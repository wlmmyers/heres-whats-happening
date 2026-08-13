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
