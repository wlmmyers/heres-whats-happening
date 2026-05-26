locals {
  secret_names = [
    "jwt-signing-key",
    "spotify-client-id",
    "spotify-client-secret",
    "spotify-token-enc-key",
    "ticketmaster-api-key",
  ]
}

resource "aws_secretsmanager_secret" "app" {
  for_each = toset(local.secret_names)
  name     = "${var.app_name_prefix}/${each.key}"

  description = "Plan 8 — seeded out-of-band; value not managed by Terraform."

  recovery_window_in_days = 7

  tags = { App = var.app_name_prefix }
}

# Placeholder version. A real value must be written via:
#   aws secretsmanager put-secret-value --secret-id hwh/jwt-signing-key --secret-string "$(openssl rand -hex 32)"
resource "aws_secretsmanager_secret_version" "app_placeholder" {
  for_each      = aws_secretsmanager_secret.app
  secret_id     = each.value.id
  secret_string = "REPLACE_ME_AFTER_APPLY"

  lifecycle {
    ignore_changes = [secret_string]
  }
}
