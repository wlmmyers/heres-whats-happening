output "alb_dns_name" {
  description = "Direct ALB hostname (for diagnostics; users hit the Route53-fronted api.<domain>)."
  value       = aws_lb.main.dns_name
}

output "api_url" {
  description = "Public HTTPS API URL."
  value       = "https://api.${var.domain_name}"
}

output "frontend_url" {
  description = "Public HTTPS site URL."
  value       = "https://${var.domain_name}"
}

output "frontend_bucket" {
  description = "S3 bucket the frontend deploy script syncs to."
  value       = aws_s3_bucket.frontend.bucket
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID for the frontend deploy script's invalidation step."
  value       = aws_cloudfront_distribution.frontend.id
}

output "db_endpoint" {
  description = "RDS endpoint (host:port)."
  value       = aws_db_instance.main.endpoint
}

output "db_master_user_secret_arn" {
  description = "ARN of the RDS-managed master password secret."
  value       = aws_db_instance.main.master_user_secret[0].secret_arn
}

output "events_queue_url" {
  value = aws_sqs_queue.events.url
}

output "interests_queue_url" {
  value = aws_sqs_queue.interests.url
}

output "post_apply_steps" {
  description = "Operator checklist after first apply."
  value       = <<-EOT
    1. Seed the Secrets Manager values that Terraform created as placeholders:
       for s in jwt-signing-key spotify-client-id spotify-client-secret spotify-token-enc-key ticketmaster-api-key; do
         aws secretsmanager put-secret-value \
           --secret-id "${var.app_name_prefix}/$s" \
           --secret-string "<real value>"
       done
       Note: spotify-token-enc-key must be 32 bytes base64-encoded (openssl rand -base64 32).

    2. Push a bootstrap image to ECR (only needed before the first ECS service deploy):
       aws ecr get-login-password --region ${var.aws_region} | docker login --username AWS \
         --password-stdin <account-id>.dkr.ecr.${var.aws_region}.amazonaws.com
       docker pull public.ecr.aws/nginx/nginx:latest
       docker tag public.ecr.aws/nginx/nginx:latest <account-id>.dkr.ecr.${var.aws_region}.amazonaws.com/${var.app_name_prefix}-app:bootstrap
       docker push <account-id>.dkr.ecr.${var.aws_region}.amazonaws.com/${var.app_name_prefix}-app:bootstrap

    3. Trigger the app pipeline (push to master, or manually start it in the AWS console).
       This builds the real Go image and rolls the api service + scheduled task defs.

    4. Run database migrations (one-off ECS task — RDS is private, so this must run
       inside the VPC; the app binary applies the embedded SQL via `app migrate`):
       SUBNETS=$(aws ec2 describe-subnets --region ${var.aws_region} \
         --filters "Name=tag:Name,Values=${var.app_name_prefix}-public-*" \
         --query 'Subnets[].SubnetId' --output text | tr '\t' ',')
       SG=$(aws ec2 describe-security-groups --region ${var.aws_region} \
         --filters "Name=group-name,Values=${var.app_name_prefix}-task-runner" \
         --query 'SecurityGroups[0].GroupId' --output text)
       aws ecs run-task --region ${var.aws_region} --cluster ${var.app_name_prefix}-cluster \
         --launch-type FARGATE --task-definition ${var.app_name_prefix}-api \
         --network-configuration "awsvpcConfiguration={subnets=[$SUBNETS],securityGroups=[$SG],assignPublicIp=ENABLED}" \
         --overrides '{"containerOverrides":[{"name":"api","command":["migrate"]}]}'
       (Migrations are embedded in the image and tracked in schema_migrations; safe to re-run.
        Check the task's exitCode (0) and the api CloudWatch log group for output.)

    5. Configure the frontend deploy script with the outputs above:
       cat > web/.env.deploy <<EOF
       S3_BUCKET=${aws_s3_bucket.frontend.bucket}
       CLOUDFRONT_DISTRIBUTION_ID=${aws_cloudfront_distribution.frontend.id}
       VITE_API_BASE_URL=https://api.${var.domain_name}
       EOF
       cd web && pnpm deploy
  EOT
}

output "email_ingest_recipient" {
  description = "Subscribe promoter newsletters to this address."
  value       = local.ingest_recipient
}

output "email_parser_ecr_repo" {
  description = "ECR repo URL for the email-parser Lambda image."
  value       = aws_ecr_repository.email_parser.repository_url
}

output "email_inbound_bucket" {
  description = "S3 bucket holding raw inbound emails (audit trail)."
  value       = aws_s3_bucket.inbound_email.bucket
}

output "email_post_apply_steps" {
  description = "Operator steps to finish wiring email ingestion after apply."
  value       = <<-EOT
    Email-newsletter ingestion — operator steps after apply:

    1. Seed the model key (Mastra reads ANTHROPIC_API_KEY):
       aws secretsmanager put-secret-value \
         --secret-id ${var.app_name_prefix}/email-llm-api-key \
         --secret-string "<your-anthropic-api-key>"

    2. Confirm DNS: inbound.${var.domain_name} MX + 3 DKIM CNAMEs resolve, and
       the SES domain identity shows "verified" in the SES console.

    3. Build + push the Lambda image via the CodeBuild project running
       ci/buildspec-lambda.yml (it then runs aws lambda update-function-code).
       For the FIRST apply, a bootstrap image must already exist at
       ${aws_ecr_repository.email_parser.repository_url}:bootstrap (or pass
       -var email_parser_image_tag=<sha> on the real deploy).

    4. Send a test newsletter to ${local.ingest_recipient}; check the
       ${var.app_name_prefix}-email-parser CloudWatch logs, then confirm a new
       row with source = email_newsletter in the events table.

    5. Watch the ${var.app_name_prefix}-email-parser-dlq-depth alarm — depth >= 1
       means emails are failing to parse.
  EOT
}
