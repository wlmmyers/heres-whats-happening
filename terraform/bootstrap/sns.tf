# SNS topic that the infra pipeline's manual-approval action publishes to.
# The email subscription must be confirmed via the link AWS sends after apply.

resource "aws_sns_topic" "infra_approval" {
  name = "${var.app_name_prefix}-infra-approval"
}

resource "aws_sns_topic_subscription" "infra_approval_email" {
  topic_arn = aws_sns_topic.infra_approval.arn
  protocol  = "email"
  endpoint  = var.approval_email
}
