variable "aws_region" {
  description = "AWS region to deploy bootstrap resources into."
  type        = string
  default     = "us-east-1"
}

variable "app_name_prefix" {
  description = "Prefix applied to most resource names."
  type        = string
  default     = "hwh"
}

variable "github_owner" {
  description = "GitHub account or org that owns the repo."
  type        = string
  default     = "wmyers"
}

variable "github_repo" {
  description = "GitHub repo name (no owner prefix)."
  type        = string
  default     = "heres-whats-happening"
}

variable "github_branch" {
  description = "Branch the pipelines source from."
  type        = string
  default     = "master"
}

variable "approval_email" {
  description = "Email address to subscribe to the infra-approval SNS topic. Required."
  type        = string
}
