# ---------------------------------------------------------------------------
# Docker Hub credentials for authenticated CI pulls
#
# CodeBuild (hwh-app-build) pulls pgvector, elasticmq, golang, and the BuildKit
# frontend from Docker Hub. Anonymous pulls share AWS's egress IPs and hit the
# per-IP rate limit; authenticating moves us to our own per-account budget.
#
# Same pattern as terraform/prod/secrets.tf: Terraform owns the container and a
# placeholder version; the real value is seeded out-of-band so it never lands
# in tfstate.
#   aws secretsmanager put-secret-value --secret-id hwh/dockerhub \
#     --secret-string '{"username":"YOUR_USER","token":"YOUR_PAT"}'
# ---------------------------------------------------------------------------

resource "aws_secretsmanager_secret" "dockerhub" {
  name                    = "${var.app_name_prefix}/dockerhub"
  description             = "Docker Hub creds for authenticated CI pulls; seeded out-of-band."
  recovery_window_in_days = 7

  tags = { App = var.app_name_prefix }
}

resource "aws_secretsmanager_secret_version" "dockerhub_placeholder" {
  secret_id     = aws_secretsmanager_secret.dockerhub.id
  secret_string = jsonencode({ username = "REPLACE_ME", token = "REPLACE_ME" })

  lifecycle {
    ignore_changes = [secret_string]
  }
}
