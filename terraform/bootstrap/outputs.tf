output "tf_state_bucket" {
  description = "S3 bucket name for the prod stack's remote state."
  value       = aws_s3_bucket.tf_state.bucket
}

output "tf_state_lock_table" {
  description = "DynamoDB table for prod state locking."
  value       = aws_dynamodb_table.tf_state_lock.name
}

output "ecr_repository_url" {
  description = "Docker pull/push URL for the app image."
  value       = aws_ecr_repository.app.repository_url
}

output "github_connection_arn" {
  description = "CodeStar Connection ARN. Must be authorized in the AWS console after apply."
  value       = aws_codestarconnections_connection.github.arn
}

output "infra_pipeline_name" {
  value = aws_codepipeline.infra.name
}

output "app_pipeline_name" {
  value = aws_codepipeline.app.name
}

output "web_pipeline_name" {
  value = aws_codepipeline.web.name
}

output "approval_topic_arn" {
  description = "SNS topic that emits manual-approval notifications. Email subscription must be confirmed."
  value       = aws_sns_topic.infra_approval.arn
}

output "pipeline_artifacts_bucket" {
  description = "S3 bucket the pipelines use to pass artifacts between stages."
  value       = aws_s3_bucket.pipeline_artifacts.bucket
}

output "post_apply_steps" {
  description = "Human-readable next-steps after bootstrap apply."
  value       = <<-EOT
    1. Authorize the GitHub CodeStar Connection:
       AWS Console → Developer Tools → Settings → Connections →
       ${aws_codestarconnections_connection.github.name} → Update pending connection.
    2. Confirm the SNS email subscription sent to ${var.approval_email}.
    3. Once Plan 8 (terraform/prod) is written, push to master; the infra
       pipeline auto-triggers and waits for your manual approval.
  EOT
}
