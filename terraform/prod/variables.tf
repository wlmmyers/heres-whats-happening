variable "aws_region" {
  description = "AWS region for all prod resources."
  type        = string
  default     = "us-east-1"

  validation {
    condition     = var.aws_region == "us-east-1"
    error_message = "aws_region must be us-east-1: the frontend ACM certificate is consumed by CloudFront, which only accepts certificates from us-east-1."
  }
}

variable "app_name_prefix" {
  description = "Prefix applied to most resource names. Must match Plan 7's bootstrap."
  type        = string
  default     = "hwh"
}

variable "domain_name" {
  description = "Apex domain. The SPA is served from this; the API from api.<domain>. Override in terraform.tfvars."
  type        = string
  default     = "example.com"
}

variable "ticketmaster_city" {
  description = "City name to pass to the Ticketmaster Discovery API."
  type        = string
  default     = "New York"
}

variable "db_instance_class" {
  description = "RDS instance class. v1 defaults to db.t4g.small."
  type        = string
  default     = "db.t4g.small"
}

variable "db_allocated_storage_gb" {
  description = "Allocated storage in GB."
  type        = number
  default     = 20
}

variable "db_backup_retention_days" {
  description = "Days of automated backups. 0 disables; v1 keeps a week."
  type        = number
  default     = 7
}

variable "ingest_workers" {
  description = "Number of worker goroutines per consumer in the api service."
  type        = number
  default     = 4
}

variable "api_cpu" {
  description = "ECS Fargate CPU units for the api task. 512 = 0.5 vCPU."
  type        = number
  default     = 256
}

variable "api_memory" {
  description = "ECS Fargate memory in MiB for the api task."
  type        = number
  default     = 512
}

variable "tei_cpu" {
  description = "ECS Fargate CPU units for the TEI task. TEI is CPU-bound and benefits from headroom."
  type        = number
  default     = 1024
}

variable "tei_memory" {
  description = "ECS Fargate memory in MiB for the TEI task."
  type        = number
  default     = 2048
}

variable "tei_image" {
  description = "TEI Docker image. Pinned to a specific digest in production."
  type        = string
  default     = "ghcr.io/huggingface/text-embeddings-inference:cpu-1.9"
}

variable "tei_model_id" {
  description = "Hugging Face model ID for TEI to serve."
  type        = string
  default     = "BAAI/bge-small-en-v1.5"
}
