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

output "database_url_secret_arn" {
  description = "ARN of the secret holding the full DATABASE_URL DSN."
  value       = aws_secretsmanager_secret.database_url.arn
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

    4. Run database migrations (one-time bootstrap from your laptop or a one-off ECS task):
       psql "${aws_secretsmanager_secret.database_url.name}" -f sql/migrations/*.up.sql
       (See README; production migrations should be a separate plan if frequent.)

    5. Configure the frontend deploy script with the outputs above:
       cat > web/.env.deploy <<EOF
       S3_BUCKET=${aws_s3_bucket.frontend.bucket}
       CLOUDFRONT_DISTRIBUTION_ID=${aws_cloudfront_distribution.frontend.id}
       VITE_API_BASE_URL=https://api.${var.domain_name}
       EOF
       cd web && pnpm deploy
  EOT
}
