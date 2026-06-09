locals {
  api_image = "${data.aws_ecr_repository.app.repository_url}:bootstrap"

  # Plain env vars — non-secret config.
  # Bootstrap defaults — Terraform sets these once. Ongoing value changes go through
  # taskdef-edit.sh; this file will drift from live values and that's expected
  api_env_vars = [
    { name = "HTTP_ADDR", value = ":8080" },
    { name = "JWT_ACCESS_TTL", value = "15m" },
    { name = "REFRESH_TTL", value = "720h" },
    { name = "LOG_LEVEL", value = "info" },
    { name = "AWS_REGION", value = var.aws_region },
    { name = "DB_HOST", value = aws_db_instance.main.address },
    { name = "DB_PORT", value = tostring(aws_db_instance.main.port) },
    { name = "DB_NAME", value = aws_db_instance.main.db_name },
    { name = "DB_SSLMODE", value = "require" },
    # ARN (not the value) of the RDS-managed master secret. The app fetches the
    # password from here on each new DB connection, so an automatic password
    # rotation is picked up as connections recycle — no task restart needed.
    # DB_PASSWORD (injected below) remains the startup/fallback credential.
    { name = "DB_SECRET_ARN", value = aws_db_instance.main.master_user_secret[0].secret_arn },
    { name = "EVENTS_QUEUE_URL", value = aws_sqs_queue.events.url },
    { name = "INTERESTS_QUEUE_URL", value = aws_sqs_queue.interests.url },
    { name = "INGEST_WORKERS", value = tostring(var.ingest_workers) },
    { name = "TICKETMASTER_CITY", value = var.ticketmaster_city },
    { name = "TEI_ENDPOINT", value = "http://tei.${var.app_name_prefix}.local" },
    { name = "ICAL_BASE_URL", value = "https://api.${var.domain_name}" },
    { name = "CORS_ALLOWED_ORIGINS", value = "https://${var.domain_name},https://www.${var.domain_name}" },
    { name = "SPOTIFY_REDIRECT_URI", value = "https://${var.domain_name}/integrations/spotify/callback" },
  ]

  # Secret env vars — pulled from Secrets Manager.
  # Bootstrap defaults — Terraform sets these once. Ongoing value changes go through
  # taskdef-edit.sh; this file will drift from live values and that's OK
  api_secrets = [
    # DB_USER/DB_PASSWORD use ECS JSON-key secret refs against the RDS-managed
    # master secret: "<secret-arn>:<json-key>:<version-stage>:<version-id>".
    # Empty version-stage/version-id => latest AWSCURRENT, so a password rotation
    # is picked up on the next task start.
    { name = "DB_USER", valueFrom = "${aws_db_instance.main.master_user_secret[0].secret_arn}:username::" },
    { name = "DB_PASSWORD", valueFrom = "${aws_db_instance.main.master_user_secret[0].secret_arn}:password::" },
    { name = "JWT_SIGNING_KEY", valueFrom = aws_secretsmanager_secret.app["jwt-signing-key"].arn },
    { name = "SPOTIFY_CLIENT_ID", valueFrom = aws_secretsmanager_secret.app["spotify-client-id"].arn },
    { name = "SPOTIFY_CLIENT_SECRET", valueFrom = aws_secretsmanager_secret.app["spotify-client-secret"].arn },
    { name = "SPOTIFY_TOKEN_ENC_KEY", valueFrom = aws_secretsmanager_secret.app["spotify-token-enc-key"].arn },
    { name = "TICKETMASTER_API_KEY", valueFrom = aws_secretsmanager_secret.app["ticketmaster-api-key"].arn },
  ]
}

resource "aws_ecs_task_definition" "api" {
  family                   = "${var.app_name_prefix}-api"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = tostring(var.api_cpu)
  memory                   = tostring(var.api_memory)
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.task.arn

  container_definitions = jsonencode([{
    name      = "api"
    image     = local.api_image
    essential = true
    command   = ["serve"]
    portMappings = [{
      containerPort = 8080
      protocol      = "tcp"
    }]
    environment = local.api_env_vars
    secrets     = local.api_secrets
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.ecs["api"].name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = "api"
      }
    }
  }])

  lifecycle {
    # Plan 7's app pipeline updates the image post-apply via aws ecs register-task-definition.
    # Don't fight it on subsequent Terraform applies.
    ignore_changes = [container_definitions]
  }
}

resource "aws_ecs_service" "api" {
  name            = "${var.app_name_prefix}-api"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.api.arn
  desired_count   = 1
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = aws_subnet.public[*].id
    security_groups  = [aws_security_group.api_task.id]
    assign_public_ip = true
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.api.arn
    container_name   = "api"
    container_port   = 8080
  }

  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200
  health_check_grace_period_seconds  = 60

  lifecycle {
    ignore_changes = [task_definition]
  }

  depends_on = [aws_lb_listener.https]
}
