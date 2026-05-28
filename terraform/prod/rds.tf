resource "aws_db_subnet_group" "main" {
  name       = "${var.app_name_prefix}-db"
  subnet_ids = aws_subnet.private[*].id
  tags       = { Name = "${var.app_name_prefix}-db" }
}

# Custom parameter group: preload pg_stat_statements + log statements > 1s for visibility.
# (pgvector needs no preload — it's enabled per-database via CREATE EXTENSION vector.)
resource "aws_db_parameter_group" "pg16" {
  name        = "${var.app_name_prefix}-pg16-pgvector"
  family      = "postgres16"
  description = "Plan 8 - Postgres 16 with pg_stat_statements preload + slow-query log."

  # pgvector does NOT need preloading — it's enabled per-database with
  # `CREATE EXTENSION vector;`. Only pg_stat_statements needs to be preloaded.
  parameter {
    name         = "shared_preload_libraries"
    value        = "pg_stat_statements"
    apply_method = "pending-reboot"
  }
  parameter {
    name  = "log_min_duration_statement"
    value = "1000"
  }
}

resource "aws_db_instance" "main" {
  identifier = "${var.app_name_prefix}-db"

  engine         = "postgres"
  engine_version = "16"
  instance_class = var.db_instance_class

  allocated_storage = var.db_allocated_storage_gb
  storage_type      = "gp3"
  storage_encrypted = true

  db_name  = "appdb"
  username = "app"

  # RDS-managed master password lives in Secrets Manager automatically.
  manage_master_user_password = true

  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.rds.id]
  parameter_group_name   = aws_db_parameter_group.pg16.name

  backup_retention_period   = var.db_backup_retention_days
  skip_final_snapshot       = false
  final_snapshot_identifier = "${var.app_name_prefix}-db-final"

  deletion_protection = true

  publicly_accessible = false

  enabled_cloudwatch_logs_exports = ["postgresql"]

  tags = { Name = "${var.app_name_prefix}-db" }
}

# Construct the full DATABASE_URL secret. The app reads DATABASE_URL as a single
# env var (per Plans 1-5); Secrets Manager can only inject one env per secret,
# so we encode the full DSN in one secret. Terraform composes it from the RDS
# endpoint + the RDS-managed password.
#
# We can't reference the password directly from the RDS-managed secret in Terraform
# (the secret value is opaque). The workaround: read the secret JSON in Terraform
# via aws_secretsmanager_secret_version data source, parse it, and write the DSN
# to a NEW secret that ECS pulls.
data "aws_secretsmanager_secret_version" "db_master" {
  secret_id  = aws_db_instance.main.master_user_secret[0].secret_arn
  depends_on = [aws_db_instance.main]
}

locals {
  db_master_password = jsondecode(data.aws_secretsmanager_secret_version.db_master.secret_string)["password"]
  database_url       = "postgres://${aws_db_instance.main.username}:${local.db_master_password}@${aws_db_instance.main.endpoint}/${aws_db_instance.main.db_name}?sslmode=require"
}

resource "aws_secretsmanager_secret" "database_url" {
  name                    = "${var.app_name_prefix}/database-url"
  description             = "Full DATABASE_URL (DSN with embedded password). Terraform-managed."
  recovery_window_in_days = 7
}

resource "aws_secretsmanager_secret_version" "database_url" {
  secret_id     = aws_secretsmanager_secret.database_url.id
  secret_string = local.database_url
}
