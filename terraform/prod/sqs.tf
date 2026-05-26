resource "aws_sqs_queue" "events_dlq" {
  name                      = "${var.app_name_prefix}-events-dlq"
  message_retention_seconds = 1209600 # 14 days
}

resource "aws_sqs_queue" "events" {
  name                       = "${var.app_name_prefix}-events-queue"
  visibility_timeout_seconds = 30
  receive_wait_time_seconds  = 20     # long polling
  message_retention_seconds  = 345600 # 4 days

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.events_dlq.arn
    maxReceiveCount     = 3
  })
}

resource "aws_sqs_queue" "interests_dlq" {
  name                      = "${var.app_name_prefix}-interests-dlq"
  message_retention_seconds = 1209600
}

resource "aws_sqs_queue" "interests" {
  name                       = "${var.app_name_prefix}-interests-queue"
  visibility_timeout_seconds = 30
  receive_wait_time_seconds  = 20
  message_retention_seconds  = 345600

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.interests_dlq.arn
    maxReceiveCount     = 3
  })
}

# Alarm when a DLQ accumulates >=1 message — investigate ASAP.
resource "aws_cloudwatch_metric_alarm" "events_dlq_depth" {
  alarm_name          = "${var.app_name_prefix}-events-dlq-depth"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 1
  dimensions          = { QueueName = aws_sqs_queue.events_dlq.name }
  alarm_description   = "Messages landed in the events DLQ. Check consumer logs."
}

resource "aws_cloudwatch_metric_alarm" "interests_dlq_depth" {
  alarm_name          = "${var.app_name_prefix}-interests-dlq-depth"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 1
  dimensions          = { QueueName = aws_sqs_queue.interests_dlq.name }
  alarm_description   = "Messages landed in the interests DLQ. Check consumer logs."
}
