resource "aws_ecs_task_definition" "tei" {
  family                   = "${var.app_name_prefix}-tei"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = tostring(var.tei_cpu)
  memory                   = tostring(var.tei_memory)
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.task.arn

  container_definitions = jsonencode([{
    name      = "tei"
    image     = var.tei_image
    essential = true
    command   = ["--model-id", var.tei_model_id]
    environment = [
      { name = "HF_ENDPOINT", value = "https://huggingface.co" },
    ]
    portMappings = [{
      containerPort = 80
      protocol      = "tcp"
    }]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.ecs["tei"].name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = "tei"
      }
    }
  }])
}

resource "aws_ecs_service" "tei" {
  name            = "${var.app_name_prefix}-tei"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.tei.arn
  desired_count   = 1

  # Fargate Spot: ~70% cheaper than on-demand. TEI is a stateless embedding server
  # behind service discovery; a Spot reclaim → ECS launches a replacement task
  # (desired_count stays 1). Callers tolerate the brief gap: the tei client retries
  # transient failures (connection refused / 5xx) with backoff, so a short blip is
  # ridden through in-request. Beyond that, the user-facing handler enqueues rather
  # than calling TEI inline and the SQS interest consumer redelivers on error (embeds
  # are idempotent), so real-time embeds retry; the nightly match job re-selects
  # unembedded/stale rows on its next run, so a longer outage only delays embeddings,
  # it doesn't drop them. capacity_provider_strategy and launch_type are mutually
  # exclusive, so this replaces the previous launch_type = "FARGATE".
  capacity_provider_strategy {
    capacity_provider = "FARGATE_SPOT"
    weight            = 1
  }

  network_configuration {
    subnets          = aws_subnet.public[*].id
    security_groups  = [aws_security_group.tei_task.id]
    assign_public_ip = true
  }

  service_registries {
    registry_arn = aws_service_discovery_service.tei.arn
  }

  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200
}
