locals {
  ecs_log_groups = [
    "api",
    "tei",
    "scrape-events-ticketmaster",
    "scrape-spotify",
    "match",
  ]
}

resource "aws_cloudwatch_log_group" "ecs" {
  for_each          = toset(local.ecs_log_groups)
  name              = "/aws/ecs/${var.app_name_prefix}/${each.key}"
  retention_in_days = 30
}
