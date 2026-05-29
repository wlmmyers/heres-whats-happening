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
