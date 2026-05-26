resource "aws_ecs_cluster" "main" {
  name = "${var.app_name_prefix}-cluster"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }
}

resource "aws_ecs_cluster_capacity_providers" "main" {
  cluster_name       = aws_ecs_cluster.main.name
  capacity_providers = ["FARGATE", "FARGATE_SPOT"]

  default_capacity_provider_strategy {
    capacity_provider = "FARGATE"
    weight            = 1
    base              = 1
  }
}

# Private DNS namespace for service discovery — used so the api service can
# resolve tei.hwh.local to TEI's task IP.
resource "aws_service_discovery_private_dns_namespace" "internal" {
  name        = "${var.app_name_prefix}.local"
  description = "Internal service discovery for ECS tasks"
  vpc         = aws_vpc.main.id
}

resource "aws_service_discovery_service" "tei" {
  name = "tei"

  dns_config {
    namespace_id = aws_service_discovery_private_dns_namespace.internal.id
    dns_records {
      type = "A"
      ttl  = 10
    }
    routing_policy = "MULTIVALUE"
  }

  health_check_custom_config {
    failure_threshold = 1
  }
}
