# Image tag the lambda CI pushes (see ci/buildspec-lambda.yml). Default is the
# bootstrap placeholder pushed before the first real deploy.
variable "email_parser_image_tag" {
  type    = string
  default = "bootstrap"
}

# DLQ for failed async invocations (poison emails).
resource "aws_sqs_queue" "email_parser_dlq" {
  name                      = "${var.app_name_prefix}-email-parser-dlq"
  message_retention_seconds = 1209600 # 14 days
}

data "aws_iam_policy_document" "email_parser_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "email_parser" {
  name               = "${var.app_name_prefix}-email-parser"
  assume_role_policy = data.aws_iam_policy_document.email_parser_assume.json
}

data "aws_iam_policy_document" "email_parser" {
  statement {
    sid       = "ReadRawEmail"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.inbound_email.arn}/*"]
  }
  statement {
    sid       = "SendToEventsQueue"
    actions   = ["sqs:SendMessage", "sqs:SendMessageBatch", "sqs:GetQueueAttributes"]
    resources = [aws_sqs_queue.events.arn]
  }
  statement {
    sid       = "WriteDLQ"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.email_parser_dlq.arn]
  }
  statement {
    sid       = "ReadModelKey"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.email_llm_key.arn]
  }
  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["arn:aws:logs:${var.aws_region}:${data.aws_caller_identity.current.account_id}:*"]
  }
}

resource "aws_iam_role_policy" "email_parser" {
  name   = "${var.app_name_prefix}-email-parser"
  role   = aws_iam_role.email_parser.id
  policy = data.aws_iam_policy_document.email_parser.json
}

resource "aws_lambda_function" "email_parser" {
  function_name = "${var.app_name_prefix}-email-parser"
  role          = aws_iam_role.email_parser.arn
  package_type  = "Image"
  image_uri     = "${aws_ecr_repository.email_parser.repository_url}:${var.email_parser_image_tag}"
  timeout       = 120
  memory_size   = 1024

  environment {
    variables = {
      EVENTS_QUEUE_URL   = aws_sqs_queue.events.url
      LLM_API_KEY_SECRET = aws_secretsmanager_secret.email_llm_key.arn
      LLM_MODEL          = "anthropic/claude-sonnet-4-5"
    }
  }
}

# Async retries + DLQ for poison emails.
# No `qualifier` -> targets $LATEST, which is what the S3 notification invokes.
# If you ever route S3 to a published version/alias, set qualifier here too.
resource "aws_lambda_function_event_invocation_config" "email_parser" {
  function_name                = aws_lambda_function.email_parser.function_name
  maximum_retry_attempts       = 2
  maximum_event_age_in_seconds = 3600
  destination_config {
    on_failure {
      destination = aws_sqs_queue.email_parser_dlq.arn
    }
  }
}

resource "aws_lambda_permission" "allow_s3_invoke" {
  statement_id  = "AllowExecutionFromS3"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.email_parser.function_name
  principal     = "s3.amazonaws.com"
  source_arn    = aws_s3_bucket.inbound_email.arn
}

resource "aws_cloudwatch_metric_alarm" "email_parser_dlq_depth" {
  alarm_name          = "${var.app_name_prefix}-email-parser-dlq-depth"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 1
  dimensions          = { QueueName = aws_sqs_queue.email_parser_dlq.name }
  alarm_description   = "Emails failed parsing and landed in the email-parser DLQ. Check Lambda logs."
}
