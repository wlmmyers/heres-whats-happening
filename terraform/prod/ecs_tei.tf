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
  launch_type     = "FARGATE"

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
