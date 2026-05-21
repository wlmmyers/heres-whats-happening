# Account ID is used to make globally unique resource names (e.g., S3 bucket).
data "aws_caller_identity" "current" {}

data "aws_region" "current" {}
