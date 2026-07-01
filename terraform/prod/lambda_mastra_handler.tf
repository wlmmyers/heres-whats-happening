# Image tag the lambda CI pushes (see ci/buildspec-lambda.yml). Default is the
# bootstrap placeholder pushed before the first real deploy.
variable "mastra_handler_image_tag" {
  type    = string
  default = "bootstrap"
}

# DLQ for failed async invocations (poison emails).
resource "aws_sqs_queue" "mastra_handler_dlq" {
  name                      = "${var.app_name_prefix}-mastra-handler-dlq"
  message_retention_seconds = 1209600 # 14 days
}

data "aws_iam_policy_document" "mastra_handler_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "mastra_handler" {
  name               = "${var.app_name_prefix}-mastra-handler"
  assume_role_policy = data.aws_iam_policy_document.mastra_handler_assume.json
}

data "aws_iam_policy_document" "mastra_handler" {
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
    resources = [aws_sqs_queue.mastra_handler_dlq.arn]
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
  statement {
    sid       = "WritePosters"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.posters.arn}/*"]
  }
}

resource "aws_iam_role_policy" "mastra_handler" {
  name   = "${var.app_name_prefix}-mastra-handler"
  role   = aws_iam_role.mastra_handler.id
  policy = data.aws_iam_policy_document.mastra_handler.json
}

resource "aws_lambda_function" "mastra_handler" {
  function_name = "${var.app_name_prefix}-mastra-handler"
  role          = aws_iam_role.mastra_handler.arn
  package_type  = "Image"
  image_uri     = "${data.aws_ecr_repository.mastra_handler.repository_url}:${var.mastra_handler_image_tag}"
  timeout       = 300
  memory_size   = 1536

  environment {
    variables = {
      EVENTS_QUEUE_URL   = aws_sqs_queue.events.url
      LLM_API_KEY_SECRET = aws_secretsmanager_secret.email_llm_key.arn
      LLM_MODEL          = "anthropic/claude-sonnet-4-5"
      POSTERS_BUCKET     = aws_s3_bucket.posters.bucket
      MAX_IMAGE_ATTEMPTS = "3"
      MAX_SVG_ATTEMPTS   = "3"
    }
  }

  # The lambda CI lane (terraform/bootstrap) deploys new images out-of-band via
  # `aws lambda update-function-code`, so don't let a later apply of this stack
  # revert image_uri back to the bootstrap tag. `mastra_handler_image_tag` only
  # seeds the very first create.
  lifecycle {
    ignore_changes = [image_uri]
  }
}

# Async retries + DLQ for poison emails.
# No `qualifier` -> targets $LATEST, which is what the S3 notification invokes.
# If you ever route S3 to a published version/alias, set qualifier here too.
resource "aws_lambda_function_event_invoke_config" "mastra_handler" {
  function_name                = aws_lambda_function.mastra_handler.function_name
  maximum_retry_attempts       = 2
  maximum_event_age_in_seconds = 3600
  destination_config {
    on_failure {
      destination = aws_sqs_queue.mastra_handler_dlq.arn
    }
  }
}

resource "aws_lambda_permission" "allow_s3_invoke" {
  statement_id  = "AllowExecutionFromS3"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.mastra_handler.function_name
  principal     = "s3.amazonaws.com"
  source_arn    = aws_s3_bucket.inbound_email.arn
}

resource "aws_cloudwatch_metric_alarm" "mastra_handler_dlq_depth" {
  alarm_name          = "${var.app_name_prefix}-mastra-handler-dlq-depth"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 1
  dimensions          = { QueueName = aws_sqs_queue.mastra_handler_dlq.name }
  alarm_description   = "Emails failed parsing and landed in the mastra-handler DLQ. Check Lambda logs."
}
