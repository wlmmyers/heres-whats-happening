# ALB: accepts 80 + 443 from the world.
resource "aws_security_group" "alb" {
  name        = "${var.app_name_prefix}-alb"
  description = "Public ALB"
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "HTTPS from internet"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  ingress {
    description = "HTTP redirect"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-alb" }
}

# api ECS task: accepts 8080 from the ALB SG only; egress everywhere.
resource "aws_security_group" "api_task" {
  name        = "${var.app_name_prefix}-api-task"
  description = "api ECS task"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "HTTP from ALB"
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-api-task" }
}

# TEI ECS task: accepts 80 from api task SG (Cloud Map service discovery resolves
# internally to private IPs).
resource "aws_security_group" "tei_task" {
  name        = "${var.app_name_prefix}-tei-task"
  description = "TEI sidecar"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "HTTP from api"
    from_port       = 80
    to_port         = 80
    protocol        = "tcp"
    security_groups = [aws_security_group.api_task.id]
  }

  ingress {
    description     = "HTTP from scheduled task runners"
    from_port       = 80
    to_port         = 80
    protocol        = "tcp"
    security_groups = [aws_security_group.task_runner.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-tei-task" }
}

# Scheduled tasks (match-job, scrapers) reuse the tei_task SG so they can call TEI
# internally. They also need outbound to RDS + the internet — egress allows both.
# (Match-job + scrapers don't accept any inbound; we just need them to call TEI
# and RDS.) We use a dedicated SG for clarity.
resource "aws_security_group" "task_runner" {
  name        = "${var.app_name_prefix}-task-runner"
  description = "Scheduled task runners (scrapers, match-job)"
  vpc_id      = aws_vpc.main.id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-task-runner" }
}

# RDS: accepts 5432 from api_task + task_runner.
resource "aws_security_group" "rds" {
  name        = "${var.app_name_prefix}-rds"
  description = "Postgres"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "Postgres from api"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.api_task.id]
  }
  ingress {
    description     = "Postgres from task runners"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.task_runner.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-rds" }
}
