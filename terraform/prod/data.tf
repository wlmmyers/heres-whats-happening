data "aws_caller_identity" "current" {}

data "aws_region" "current" {}

# Plan 7 created this ECR repo. The data lookup means we don't duplicate the resource
# definition here — the bootstrap stack owns it.
data "aws_ecr_repository" "app" {
  name = "${var.app_name_prefix}-app"
}

# Public hosted zone for the domain. Must be created manually before applying this stack.
data "aws_route53_zone" "primary" {
  name         = var.domain_name
  private_zone = false
}

# AWS-managed default VPC's AZs (used to pick AZ names for our private subnets).
data "aws_availability_zones" "available" {
  state = "available"
}
