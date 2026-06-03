locals {
  # Same env shape as api — the scheduled tasks share most config but run different subcommands.
  scheduled_env_vars = local.api_env_vars
  scheduled_secrets  = local.api_secrets
  scheduled_image    = local.api_image

  schedules = {
    "scrape-events-ticketmaster" = {
      command   = ["scrape", "events", "--source=ticketmaster"]
      schedule  = "cron(0 0 * * ? *)" # 00:00 UTC daily
      log_group = "scrape-events-ticketmaster"
    }
    "scrape-spotify" = {
      command   = ["scrape", "spotify"]
      schedule  = "cron(0 0 * * ? *)" # 00:00 UTC daily
      log_group = "scrape-spotify"
    }
    "match" = {
      command   = ["match"]
      schedule  = "rate(4 hours)"
      log_group = "match"
    }
  }
}

resource "aws_ecs_task_definition" "scheduled" {
  for_each = local.schedules

  family                   = "${var.app_name_prefix}-${each.key}"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "512"
  memory                   = "1024"
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.task.arn

  container_definitions = jsonencode([{
    name        = each.key
    image       = local.scheduled_image
    essential   = true
    command     = each.value.command
    environment = local.scheduled_env_vars
    secrets     = local.scheduled_secrets
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.ecs[each.value.log_group].name
        awslogs-region        = var.aws_region
        awslogs-stream-prefix = each.key
      }
    }
  }])

  lifecycle {
    ignore_changes = [container_definitions]
  }
}

resource "aws_scheduler_schedule" "scheduled" {
  for_each = local.schedules

  name                = "${var.app_name_prefix}-${each.key}"
  schedule_expression = each.value.schedule

  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = aws_ecs_cluster.main.arn
    role_arn = aws_iam_role.scheduler.arn

    ecs_parameters {
      # family-only reference — uses the LATEST revision automatically.
      task_definition_arn = "arn:aws:ecs:${var.aws_region}:${data.aws_caller_identity.current.account_id}:task-definition/${aws_ecs_task_definition.scheduled[each.key].family}"
      launch_type         = "FARGATE"
      task_count          = 1

      network_configuration {
        subnets          = aws_subnet.public[*].id
        security_groups  = [aws_security_group.task_runner.id]
        assign_public_ip = true
      }
    }

    retry_policy {
      maximum_event_age_in_seconds = 3600
      maximum_retry_attempts       = 1
    }
  }
}
