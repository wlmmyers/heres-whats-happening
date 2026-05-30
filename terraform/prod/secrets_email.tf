resource "aws_secretsmanager_secret" "email_llm_key" {
  name                    = "${var.app_name_prefix}/email-llm-api-key"
  description             = "Anthropic API key for the email-parser Lambda. Seeded out-of-band."
  recovery_window_in_days = 7
  tags                    = { App = var.app_name_prefix }
}

# Placeholder version; the real key is written post-apply via:
#   aws secretsmanager put-secret-value --secret-id hwh/email-llm-api-key --secret-string "<key>"
resource "aws_secretsmanager_secret_version" "email_llm_key_placeholder" {
  secret_id     = aws_secretsmanager_secret.email_llm_key.id
  secret_string = "REPLACE_ME_AFTER_APPLY"
  lifecycle {
    ignore_changes = [secret_string]
  }
}
