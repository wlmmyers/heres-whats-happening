# Plan 8 — Terraform Prod Infrastructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Author the `terraform/prod/` stack that creates the full v1 AWS runtime — VPC, ALB, ECS cluster, api service, TEI sidecar, three scheduled tasks (event-scraper, spotify-scraper, match-job), RDS Postgres with pgvector, SQS queues, Secrets Manager entries, S3 + CloudFront for the frontend, Route53 + ACM for both `example.com` and `api.example.com`. Plan 7's infra pipeline applies this stack on every master push (after manual approval).

**Architecture:** Single Terraform root module under `terraform/prod/` using the S3 + DynamoDB backend created by Plan 7's bootstrap. Resources split per-file by concern (vpc/secrets/rds/sqs/alb/ecs/frontend/dns/iam/cloudwatch). One AWS provider in `us-east-1` (CloudFront's required cert region happens to match the rest of the infra, so no aliased provider needed). ECS task definitions are versioned but have `lifecycle.ignore_changes = [container_definitions[0].image]` so Plan 7's app pipeline can swap in new image revisions without Terraform fighting it. Initial deployment uses `:bootstrap` placeholders for the app image; the first app-pipeline run replaces them.

**Tech Stack:** Terraform 1.7+ · AWS provider ~> 5.0 · pgvector via custom DB parameter group · EventBridge Scheduler · ECS Fargate · Cloud Map service discovery for internal TEI · CloudFront with S3 origin for the SPA.

---

## File Structure

```
terraform/prod/
├── backend.tf                          # S3 + DynamoDB backend (Plan 7's outputs)
├── versions.tf                         # required_version + AWS provider pin
├── variables.tf                        # domain_name + sensitive Spotify/Ticketmaster vars
├── providers.tf                        # aws provider + default_tags
├── data.tf                             # caller_identity, region, hosted zone, ECR repo
├── terraform.tfvars.example
├── vpc.tf                              # VPC, subnets (public x2 + private x2), NAT, IGW, route tables
├── security_groups.tf                  # ALB SG, ECS task SG, RDS SG, TEI SG
├── secrets.tf                          # Secrets Manager resources (ignore_changes on value)
├── rds.tf                              # subnet group + pgvector parameter group + db instance + DSN secret
├── sqs.tf                              # 2 queues + 2 DLQs with redrive policies
├── acm.tf                              # ACM cert (validated via Route53) for api.example.com + example.com
├── alb.tf                              # ALB + target group + HTTPS listener + HTTP→HTTPS redirect
├── ecs_cluster.tf                      # ECS cluster + Cloud Map private namespace
├── cloudwatch.tf                       # log groups (1 per service/task)
├── iam.tf                              # task execution role + task role + scheduler role
├── ecs_api.tf                          # api task definition + service
├── ecs_tei.tf                          # tei task definition + service (internal Cloud Map)
├── ecs_schedules.tf                    # 3 task defs (event-scraper, spotify-scraper, match-job) + EventBridge schedules
├── frontend.tf                         # S3 bucket + CloudFront distribution + OAC
├── route53.tf                          # A records for api.example.com + apex/www + ACM validation records
└── outputs.tf                          # bucket name, distribution id, ALB DNS, etc.
```

Modify too:
- `ci/buildspec-app.yml` — extend so the deploy phase also re-registers the three scheduled-task families (Plan 7's version only updates the api service).

---

## Prerequisites

Before running `terraform apply`, the developer must:

1. **Plan 7 bootstrap is applied** — S3 state bucket + DynamoDB lock + ECR repo exist.
2. **Domain registered** — own a real domain (e.g., `example.com`). For the placeholder default, the plan uses `example.com` but the user MUST override `domain_name` in `terraform.tfvars` before deploying.
3. **Route53 public hosted zone exists** — for the domain. The Terraform `data "aws_route53_zone"` lookup will fail otherwise. Create it manually in the AWS console, update the registrar's nameservers, and wait for propagation (~minutes).
4. **`.tfvars` populated with sensitive values** — Spotify client ID/secret, Ticketmaster API key, etc. The plan also requires a base64-encoded 32-byte Spotify token encryption key and a long random JWT signing key.
5. **At least one app image in ECR** — the app pipeline must have run at least once so a `:bootstrap` or commit-SHA tag exists. If not, the first ECS service deployment fails because the image doesn't exist. Workaround in Task 11 step 3: push a stub `:bootstrap` tag manually before `terraform apply`.

---

### Task 1: Foundation — backend, versions, variables, providers, data, tfvars.example

**Files:**
- Create: `terraform/prod/backend.tf`
- Create: `terraform/prod/versions.tf`
- Create: `terraform/prod/variables.tf`
- Create: `terraform/prod/providers.tf`
- Create: `terraform/prod/data.tf`
- Create: `terraform/prod/terraform.tfvars.example`

- [ ] **Step 1: Write `terraform/prod/backend.tf`**

The bucket name + lock table name come from Plan 7's outputs. They follow the pattern `hwh-tf-state-<account-id>` and `hwh-tf-state-lock`. The bucket name has a literal account ID in it; we use a partial-config approach where the bucket is passed in via `terraform init -backend-config=...` at runtime. For the buildspec (Plan 7 task 11), the init step already runs without explicit backend-config args — so we hardcode the bucket here. The buildspec needs to be told the right bucket via env. Simpler: hardcode the account ID via a placeholder + replace via `terraform init -backend-config="bucket=..."`.

Cleanest path: the developer fills the bucket name in `backend.tf` once after Plan 7's bootstrap apply. So we ship a template-like file with the literal placeholder:

```hcl
terraform {
  backend "s3" {
    # Fill in <ACCOUNT_ID> after Plan 7's bootstrap apply prints `tf_state_bucket`.
    # Or pass via: terraform init -backend-config="bucket=hwh-tf-state-<ACCOUNT_ID>"
    bucket         = "hwh-tf-state-REPLACE_WITH_ACCOUNT_ID"
    key            = "prod/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "hwh-tf-state-lock"
    encrypt        = true
  }
}
```

The CI pipeline's `terraform init -input=false` will pick up this config as long as the bucket name resolves. The developer-local apply works the same way.

- [ ] **Step 2: Write `terraform/prod/versions.tf`**

```hcl
terraform {
  required_version = "~> 1.7"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}
```

- [ ] **Step 3: Write `terraform/prod/variables.tf`**

```hcl
variable "aws_region" {
  description = "AWS region for all prod resources."
  type        = string
  default     = "us-east-1"
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

variable "default_city_slug" {
  description = "Slug of the default city seeded in migration 0001. The API uses this for new signups."
  type        = string
  default     = "v1-city"
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
  default     = 512
}

variable "api_memory" {
  description = "ECS Fargate memory in MiB for the api task."
  type        = number
  default     = 1024
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
  default     = "ghcr.io/huggingface/text-embeddings-inference:cpu-1.5"
}

variable "tei_model_id" {
  description = "Hugging Face model ID for TEI to serve."
  type        = string
  default     = "BAAI/bge-small-en-v1.5"
}
```

- [ ] **Step 4: Write `terraform/prod/providers.tf`**

```hcl
provider "aws" {
  region = var.aws_region
  default_tags {
    tags = {
      Project   = "heres-whats-happening"
      Stack     = "prod"
      ManagedBy = "terraform"
    }
  }
}
```

- [ ] **Step 5: Write `terraform/prod/data.tf`**

```hcl
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
```

- [ ] **Step 6: Write `terraform/prod/terraform.tfvars.example`**

```hcl
# Copy to terraform.tfvars and fill in.
# Required overrides:
domain_name = "your-actual-domain.com"

# Optional overrides:
# aws_region              = "us-east-1"
# app_name_prefix         = "hwh"
# ticketmaster_city       = "New York"
# db_instance_class       = "db.t4g.small"
# db_backup_retention_days = 7
# ingest_workers          = 4
```

- [ ] **Step 7: Verify**

```bash
cd terraform/prod
terraform fmt -check
terraform init -backend=false
terraform validate
```

Expected: `Success! The configuration is valid.`

If `terraform init` is told the backend bucket doesn't exist (because Plan 7 wasn't applied yet, OR the user hasn't replaced `REPLACE_WITH_ACCOUNT_ID`), that's fine for `-backend=false`. `validate` only checks syntax.

- [ ] **Step 8: Commit**

```bash
git add terraform/prod/backend.tf terraform/prod/versions.tf terraform/prod/variables.tf terraform/prod/providers.tf terraform/prod/data.tf terraform/prod/terraform.tfvars.example
git commit -m "feat(terraform/prod): foundation — backend, versions, variables, provider, data"
```

---

### Task 2: VPC + subnets + NAT + IGW + route tables

**Files:**
- Create: `terraform/prod/vpc.tf`

- [ ] **Step 1: Write `terraform/prod/vpc.tf`**

```hcl
locals {
  azs              = slice(data.aws_availability_zones.available.names, 0, 2)
  vpc_cidr         = "10.0.0.0/16"
  public_subnet_cidrs  = ["10.0.0.0/24", "10.0.1.0/24"]
  private_subnet_cidrs = ["10.0.10.0/24", "10.0.11.0/24"]
}

resource "aws_vpc" "main" {
  cidr_block           = local.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true
  tags = { Name = "${var.app_name_prefix}-vpc" }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id
  tags   = { Name = "${var.app_name_prefix}-igw" }
}

resource "aws_subnet" "public" {
  count                   = 2
  vpc_id                  = aws_vpc.main.id
  cidr_block              = local.public_subnet_cidrs[count.index]
  availability_zone       = local.azs[count.index]
  map_public_ip_on_launch = true
  tags = { Name = "${var.app_name_prefix}-public-${local.azs[count.index]}" }
}

resource "aws_subnet" "private" {
  count             = 2
  vpc_id            = aws_vpc.main.id
  cidr_block        = local.private_subnet_cidrs[count.index]
  availability_zone = local.azs[count.index]
  tags = { Name = "${var.app_name_prefix}-private-${local.azs[count.index]}" }
}

# Single NAT gateway (v1 cost optimization — a per-AZ NAT is best practice but
# doubles cost; one NAT is acceptable until we hit AZ-failure concerns).
resource "aws_eip" "nat" {
  domain = "vpc"
  tags = { Name = "${var.app_name_prefix}-nat-eip" }
}

resource "aws_nat_gateway" "main" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public[0].id
  tags = { Name = "${var.app_name_prefix}-nat" }
  depends_on = [aws_internet_gateway.main]
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }
  tags = { Name = "${var.app_name_prefix}-public-rt" }
}

resource "aws_route_table" "private" {
  vpc_id = aws_vpc.main.id
  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.main.id
  }
  tags = { Name = "${var.app_name_prefix}-private-rt" }
}

resource "aws_route_table_association" "public" {
  count          = 2
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table_association" "private" {
  count          = 2
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private.id
}
```

- [ ] **Step 2: Verify**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
```

- [ ] **Step 3: Commit**

```bash
git add terraform/prod/vpc.tf
git commit -m "feat(terraform/prod): VPC, subnets, NAT, IGW, route tables"
```

---

### Task 3: Security groups

**Files:**
- Create: `terraform/prod/security_groups.tf`

- [ ] **Step 1: Write `terraform/prod/security_groups.tf`**

```hcl
# ALB: accepts 80 + 443 from the world.
resource "aws_security_group" "alb" {
  name        = "${var.app_name_prefix}-alb"
  description = "Public ALB"
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "HTTPS from internet"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  ingress {
    description = "HTTP redirect"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-alb" }
}

# api ECS task: accepts 8080 from the ALB SG only; egress everywhere.
resource "aws_security_group" "api_task" {
  name        = "${var.app_name_prefix}-api-task"
  description = "api ECS task"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "HTTP from ALB"
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-api-task" }
}

# TEI ECS task: accepts 80 from api task SG (Cloud Map service discovery resolves
# internally to private IPs).
resource "aws_security_group" "tei_task" {
  name        = "${var.app_name_prefix}-tei-task"
  description = "TEI sidecar"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "HTTP from api"
    from_port       = 80
    to_port         = 80
    protocol        = "tcp"
    security_groups = [aws_security_group.api_task.id]
  }

  ingress {
    description = "HTTP from match-job + scrapers (same SG once they're launched)"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    self        = true
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-tei-task" }
}

# Scheduled tasks (match-job, scrapers) reuse the tei_task SG so they can call TEI
# internally. They also need outbound to RDS + the internet — egress allows both.
# (Match-job + scrapers don't accept any inbound; we just need them to call TEI
# and RDS.) We use a dedicated SG for clarity.
resource "aws_security_group" "task_runner" {
  name        = "${var.app_name_prefix}-task-runner"
  description = "Scheduled task runners (scrapers, match-job)"
  vpc_id      = aws_vpc.main.id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-task-runner" }
}

# Open TEI ingress to the task_runner SG (match-job calls TEI).
resource "aws_security_group_rule" "tei_from_task_runner" {
  type                     = "ingress"
  from_port                = 80
  to_port                  = 80
  protocol                 = "tcp"
  security_group_id        = aws_security_group.tei_task.id
  source_security_group_id = aws_security_group.task_runner.id
  description              = "HTTP from scheduled task runners"
}

# RDS: accepts 5432 from api_task + task_runner.
resource "aws_security_group" "rds" {
  name        = "${var.app_name_prefix}-rds"
  description = "Postgres"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "Postgres from api"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.api_task.id]
  }
  ingress {
    description     = "Postgres from task runners"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.task_runner.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.app_name_prefix}-rds" }
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/security_groups.tf
git commit -m "feat(terraform/prod): security groups (ALB, api, TEI, task runner, RDS)"
```

---

### Task 4: Secrets Manager resources

**Files:**
- Create: `terraform/prod/secrets.tf`

These are the "shell" secret resources. Their values are seeded out-of-band by the developer after `terraform apply` (the plan's design doc explicitly calls this out — secret values never enter Terraform state). The `ignore_changes = [secret_string]` lifecycle means manual rotations don't trigger drift.

The RDS password is the one exception — Plan 9's `rds.tf` (next task) lets RDS itself manage that password in a separate Secrets Manager secret via `manage_master_user_password = true`. So we don't create a manual DB-password secret here.

- [ ] **Step 1: Write `terraform/prod/secrets.tf`**

```hcl
locals {
  secret_names = [
    "jwt-signing-key",
    "spotify-client-id",
    "spotify-client-secret",
    "spotify-token-enc-key",
    "ticketmaster-api-key",
  ]
}

resource "aws_secretsmanager_secret" "app" {
  for_each = toset(local.secret_names)
  name     = "${var.app_name_prefix}/${each.key}"

  description = "Plan 8 — seeded out-of-band; value not managed by Terraform."

  recovery_window_in_days = 7

  tags = { App = var.app_name_prefix }
}

# Placeholder version. A real value must be written via:
#   aws secretsmanager put-secret-value --secret-id hwh/jwt-signing-key --secret-string "$(openssl rand -hex 32)"
resource "aws_secretsmanager_secret_version" "app_placeholder" {
  for_each      = aws_secretsmanager_secret.app
  secret_id     = each.value.id
  secret_string = "REPLACE_ME_AFTER_APPLY"

  lifecycle {
    ignore_changes = [secret_string]
  }
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/secrets.tf
git commit -m "feat(terraform/prod): Secrets Manager resources (placeholder values; rotate out-of-band)"
```

---

### Task 5: RDS Postgres + pgvector

**Files:**
- Create: `terraform/prod/rds.tf`

- [ ] **Step 1: Write `terraform/prod/rds.tf`**

```hcl
resource "aws_db_subnet_group" "main" {
  name       = "${var.app_name_prefix}-db"
  subnet_ids = aws_subnet.private[*].id
  tags = { Name = "${var.app_name_prefix}-db" }
}

# Custom parameter group: pre-load pgvector + log statements > 1s for visibility.
resource "aws_db_parameter_group" "pg16" {
  name        = "${var.app_name_prefix}-pg16-pgvector"
  family      = "postgres16"
  description = "Plan 8 — Postgres 16 with pgvector preload + slow-query log."

  parameter {
    name  = "shared_preload_libraries"
    value = "pg_stat_statements,vector"
    apply_method = "pending-reboot"
  }
  parameter {
    name  = "log_min_duration_statement"
    value = "1000"
  }
}

resource "aws_db_instance" "main" {
  identifier = "${var.app_name_prefix}-db"

  engine               = "postgres"
  engine_version       = "16"
  instance_class       = var.db_instance_class
  allocated_storage    = var.db_allocated_storage_gb
  storage_type         = "gp3"
  storage_encrypted    = true

  db_name              = "appdb"
  username             = "app"

  # RDS-managed master password lives in Secrets Manager automatically.
  manage_master_user_password = true

  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.rds.id]
  parameter_group_name   = aws_db_parameter_group.pg16.name

  backup_retention_period = var.db_backup_retention_days
  skip_final_snapshot     = false
  final_snapshot_identifier = "${var.app_name_prefix}-db-final"

  deletion_protection = true

  publicly_accessible = false

  enabled_cloudwatch_logs_exports = ["postgresql"]

  tags = { Name = "${var.app_name_prefix}-db" }
}

# Construct the full DATABASE_URL secret. The app reads DATABASE_URL as a single
# env var (per Plans 1-5); Secrets Manager can only inject one env per secret,
# so we encode the full DSN in one secret. Terraform composes it from the RDS
# endpoint + the RDS-managed password.
#
# We can't reference the password directly from the RDS-managed secret in Terraform
# (the secret value is opaque). The workaround: read the secret JSON in Terraform
# via aws_secretsmanager_secret_version data source, parse it, and write the DSN
# to a NEW secret that ECS pulls.
data "aws_secretsmanager_secret_version" "db_master" {
  secret_id  = aws_db_instance.main.master_user_secret[0].secret_arn
  depends_on = [aws_db_instance.main]
}

locals {
  db_master_password = jsondecode(data.aws_secretsmanager_secret_version.db_master.secret_string)["password"]
  database_url = "postgres://${aws_db_instance.main.username}:${local.db_master_password}@${aws_db_instance.main.endpoint}/${aws_db_instance.main.db_name}?sslmode=require"
}

resource "aws_secretsmanager_secret" "database_url" {
  name        = "${var.app_name_prefix}/database-url"
  description = "Full DATABASE_URL (DSN with embedded password). Terraform-managed."
  recovery_window_in_days = 7
}

resource "aws_secretsmanager_secret_version" "database_url" {
  secret_id     = aws_secretsmanager_secret.database_url.id
  secret_string = local.database_url
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/rds.tf
git commit -m "feat(terraform/prod): RDS Postgres 16 + pgvector + DATABASE_URL secret"
```

---

### Task 6: SQS queues + DLQs

**Files:**
- Create: `terraform/prod/sqs.tf`

- [ ] **Step 1: Write `terraform/prod/sqs.tf`**

```hcl
resource "aws_sqs_queue" "events_dlq" {
  name                      = "${var.app_name_prefix}-events-dlq"
  message_retention_seconds = 1209600 # 14 days
}

resource "aws_sqs_queue" "events" {
  name                       = "${var.app_name_prefix}-events-queue"
  visibility_timeout_seconds = 30
  receive_wait_time_seconds  = 20 # long polling
  message_retention_seconds  = 345600 # 4 days

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.events_dlq.arn
    maxReceiveCount     = 3
  })
}

resource "aws_sqs_queue" "interests_dlq" {
  name                      = "${var.app_name_prefix}-interests-dlq"
  message_retention_seconds = 1209600
}

resource "aws_sqs_queue" "interests" {
  name                       = "${var.app_name_prefix}-interests-queue"
  visibility_timeout_seconds = 30
  receive_wait_time_seconds  = 20
  message_retention_seconds  = 345600

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.interests_dlq.arn
    maxReceiveCount     = 3
  })
}

# Alarm when a DLQ accumulates ≥1 message — investigate ASAP.
resource "aws_cloudwatch_metric_alarm" "events_dlq_depth" {
  alarm_name          = "${var.app_name_prefix}-events-dlq-depth"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 1
  dimensions          = { QueueName = aws_sqs_queue.events_dlq.name }
  alarm_description   = "Messages landed in the events DLQ. Check consumer logs."
}

resource "aws_cloudwatch_metric_alarm" "interests_dlq_depth" {
  alarm_name          = "${var.app_name_prefix}-interests-dlq-depth"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 1
  dimensions          = { QueueName = aws_sqs_queue.interests_dlq.name }
  alarm_description   = "Messages landed in the interests DLQ. Check consumer logs."
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/sqs.tf
git commit -m "feat(terraform/prod): SQS events + interests queues with DLQs and depth alarms"
```

---

### Task 7: ACM certificates

**Files:**
- Create: `terraform/prod/acm.tf`

Two certificates:
- One for `api.<domain>` (used by ALB; must be in same region as ALB = us-east-1).
- One for `<domain>` + `www.<domain>` (used by CloudFront; must be in us-east-1 — which is also our chosen region, so no aliased provider needed).

- [ ] **Step 1: Write `terraform/prod/acm.tf`**

```hcl
# ALB cert: api.<domain>
resource "aws_acm_certificate" "api" {
  domain_name       = "api.${var.domain_name}"
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_route53_record" "api_cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.api.domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  }
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = each.value.name
  type    = each.value.type
  ttl     = 60
  records = [each.value.record]
}

resource "aws_acm_certificate_validation" "api" {
  certificate_arn         = aws_acm_certificate.api.arn
  validation_record_fqdns = [for r in aws_route53_record.api_cert_validation : r.fqdn]
}

# Frontend cert: <domain> + www.<domain>
resource "aws_acm_certificate" "frontend" {
  domain_name               = var.domain_name
  subject_alternative_names = ["www.${var.domain_name}"]
  validation_method         = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_route53_record" "frontend_cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.frontend.domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  }
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = each.value.name
  type    = each.value.type
  ttl     = 60
  records = [each.value.record]
}

resource "aws_acm_certificate_validation" "frontend" {
  certificate_arn         = aws_acm_certificate.frontend.arn
  validation_record_fqdns = [for r in aws_route53_record.frontend_cert_validation : r.fqdn]
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/acm.tf
git commit -m "feat(terraform/prod): ACM certs for api + frontend with DNS validation"
```

---

### Task 8: ALB + target group + HTTPS listener

**Files:**
- Create: `terraform/prod/alb.tf`

- [ ] **Step 1: Write `terraform/prod/alb.tf`**

```hcl
resource "aws_lb" "main" {
  name               = "${var.app_name_prefix}-alb"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = aws_subnet.public[*].id

  idle_timeout       = 60
  enable_http2       = true

  tags = { Name = "${var.app_name_prefix}-alb" }
}

resource "aws_lb_target_group" "api" {
  name        = "${var.app_name_prefix}-api"
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = aws_vpc.main.id

  health_check {
    path                = "/healthz"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    interval            = 15
    timeout             = 5
    matcher             = "200"
  }

  deregistration_delay = 30
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.main.arn
  port              = "443"
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = aws_acm_certificate_validation.api.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.api.arn
  }
}

resource "aws_lb_listener" "http_redirect" {
  load_balancer_arn = aws_lb.main.arn
  port              = "80"
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/alb.tf
git commit -m "feat(terraform/prod): ALB + target group + HTTPS listener + HTTP redirect"
```

---

### Task 9: ECS cluster + Cloud Map namespace

**Files:**
- Create: `terraform/prod/ecs_cluster.tf`

- [ ] **Step 1: Write `terraform/prod/ecs_cluster.tf`**

```hcl
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
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/ecs_cluster.tf
git commit -m "feat(terraform/prod): ECS cluster + Cloud Map namespace + TEI service discovery"
```

---

### Task 10: IAM roles (task execution, task, scheduler)

**Files:**
- Create: `terraform/prod/iam.tf`

- [ ] **Step 1: Write `terraform/prod/iam.tf`**

```hcl
# ---------------------------------------------------------------------------
# Task execution role — ECS uses this to pull images, write logs, and pull
# secrets into the container at task start time. Same role for all tasks.
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "task_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "task_execution" {
  name               = "${var.app_name_prefix}-task-execution"
  assume_role_policy = data.aws_iam_policy_document.task_assume.json
}

resource "aws_iam_role_policy_attachment" "task_execution_managed" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Allow execution role to read the secrets we built (ECS injects them).
data "aws_iam_policy_document" "task_execution_secrets" {
  statement {
    actions = ["secretsmanager:GetSecretValue"]
    resources = concat(
      [aws_secretsmanager_secret.database_url.arn],
      [for s in aws_secretsmanager_secret.app : s.arn],
      [aws_db_instance.main.master_user_secret[0].secret_arn],
    )
  }
}

resource "aws_iam_role_policy" "task_execution_secrets" {
  role   = aws_iam_role.task_execution.id
  policy = data.aws_iam_policy_document.task_execution_secrets.json
}

# ---------------------------------------------------------------------------
# Task role — what the running container can do (SQS, etc.). Distinct from
# the execution role, which is just for ECS-level operations at task start.
# ---------------------------------------------------------------------------

resource "aws_iam_role" "task" {
  name               = "${var.app_name_prefix}-task"
  assume_role_policy = data.aws_iam_policy_document.task_assume.json
}

data "aws_iam_policy_document" "task" {
  statement {
    sid     = "SQSSendReceiveDelete"
    actions = [
      "sqs:SendMessage",
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:GetQueueAttributes",
      "sqs:ChangeMessageVisibility",
    ]
    resources = [
      aws_sqs_queue.events.arn,
      aws_sqs_queue.events_dlq.arn,
      aws_sqs_queue.interests.arn,
      aws_sqs_queue.interests_dlq.arn,
    ]
  }
}

resource "aws_iam_role_policy" "task" {
  role   = aws_iam_role.task.id
  policy = data.aws_iam_policy_document.task.json
}

# ---------------------------------------------------------------------------
# Scheduler role — EventBridge Scheduler assumes this to call ECS RunTask.
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "scheduler_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["scheduler.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "scheduler" {
  name               = "${var.app_name_prefix}-scheduler"
  assume_role_policy = data.aws_iam_policy_document.scheduler_assume.json
}

data "aws_iam_policy_document" "scheduler" {
  statement {
    actions   = ["ecs:RunTask"]
    resources = ["*"] # task def ARNs are versioned; broad allow is acceptable for v1
  }
  statement {
    actions   = ["iam:PassRole"]
    resources = [aws_iam_role.task_execution.arn, aws_iam_role.task.arn]
    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "scheduler" {
  role   = aws_iam_role.scheduler.id
  policy = data.aws_iam_policy_document.scheduler.json
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/iam.tf
git commit -m "feat(terraform/prod): IAM roles (task execution + task + scheduler)"
```

---

### Task 11: CloudWatch log groups

**Files:**
- Create: `terraform/prod/cloudwatch.tf`

- [ ] **Step 1: Write `terraform/prod/cloudwatch.tf`**

```hcl
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
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/cloudwatch.tf
git commit -m "feat(terraform/prod): CloudWatch log groups for ECS tasks"
```

---

### Task 12: api ECS service + task definition

**Files:**
- Create: `terraform/prod/ecs_api.tf`

- [ ] **Step 1: Write `terraform/prod/ecs_api.tf`**

```hcl
locals {
  api_image = "${data.aws_ecr_repository.app.repository_url}:bootstrap"

  # Plain env vars — non-secret config.
  api_env_vars = [
    { name = "HTTP_ADDR", value = ":8080" },
    { name = "JWT_ACCESS_TTL", value = "15m" },
    { name = "REFRESH_TTL", value = "720h" },
    { name = "LOG_LEVEL", value = "info" },
    { name = "AWS_REGION", value = var.aws_region },
    { name = "EVENTS_QUEUE_URL", value = aws_sqs_queue.events.url },
    { name = "INTERESTS_QUEUE_URL", value = aws_sqs_queue.interests.url },
    { name = "INGEST_WORKERS", value = tostring(var.ingest_workers) },
    { name = "TICKETMASTER_CITY", value = var.ticketmaster_city },
    { name = "TEI_ENDPOINT", value = "http://tei.${var.app_name_prefix}.local" },
    { name = "ICAL_BASE_URL", value = "https://api.${var.domain_name}" },
    { name = "CORS_ALLOWED_ORIGINS", value = "https://${var.domain_name},https://www.${var.domain_name}" },
    { name = "SPOTIFY_REDIRECT_URI", value = "https://api.${var.domain_name}/integrations/spotify/callback" },
  ]

  # Secret env vars — pulled from Secrets Manager.
  api_secrets = [
    { name = "DATABASE_URL", valueFrom = aws_secretsmanager_secret.database_url.arn },
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
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.api_task.id]
    assign_public_ip = false
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
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/ecs_api.tf
git commit -m "feat(terraform/prod): api ECS task definition + service"
```

---

### Task 13: TEI service + task definition

**Files:**
- Create: `terraform/prod/ecs_tei.tf`

- [ ] **Step 1: Write `terraform/prod/ecs_tei.tf`**

```hcl
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
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.tei_task.id]
    assign_public_ip = false
  }

  service_registries {
    registry_arn = aws_service_discovery_service.tei.arn
  }

  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/ecs_tei.tf
git commit -m "feat(terraform/prod): TEI ECS service with Cloud Map service discovery"
```

---

### Task 14: Scheduled task definitions + EventBridge schedules

**Files:**
- Create: `terraform/prod/ecs_schedules.tf`

Three scheduled tasks share the same container env shape as the api service but with different `command` arrays. The match-job runs at 02:00; scrapers run at 00:00.

- [ ] **Step 1: Write `terraform/prod/ecs_schedules.tf`**

```hcl
locals {
  # Same env shape as api — the scheduled tasks share most config but run different subcommands.
  scheduled_env_vars  = local.api_env_vars
  scheduled_secrets   = local.api_secrets
  scheduled_image     = local.api_image

  schedules = {
    "scrape-events-ticketmaster" = {
      command    = ["scrape", "events", "--source=ticketmaster"]
      schedule   = "cron(0 0 * * ? *)" # 00:00 UTC daily
      log_group  = "scrape-events-ticketmaster"
    }
    "scrape-spotify" = {
      command    = ["scrape", "spotify"]
      schedule   = "cron(0 0 * * ? *)" # 00:00 UTC daily
      log_group  = "scrape-spotify"
    }
    "match" = {
      command    = ["match"]
      schedule   = "cron(0 2 * * ? *)" # 02:00 UTC daily
      log_group  = "match"
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
    name      = each.key
    image     = local.scheduled_image
    essential = true
    command   = each.value.command
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
        subnets          = aws_subnet.private[*].id
        security_groups  = [aws_security_group.task_runner.id]
        assign_public_ip = false
      }
    }

    retry_policy {
      maximum_event_age_in_seconds = 3600
      maximum_retry_attempts       = 1
    }
  }
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/ecs_schedules.tf
git commit -m "feat(terraform/prod): scheduled task defs + EventBridge schedules (scrapers, match-job)"
```

---

### Task 15: Frontend — S3 + CloudFront

**Files:**
- Create: `terraform/prod/frontend.tf`

- [ ] **Step 1: Write `terraform/prod/frontend.tf`**

```hcl
resource "aws_s3_bucket" "frontend" {
  bucket = "${var.app_name_prefix}-frontend-${data.aws_caller_identity.current.account_id}"
}

resource "aws_s3_bucket_public_access_block" "frontend" {
  bucket                  = aws_s3_bucket.frontend.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_versioning" "frontend" {
  bucket = aws_s3_bucket.frontend.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "frontend" {
  bucket = aws_s3_bucket.frontend.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_cloudfront_origin_access_control" "frontend" {
  name                              = "${var.app_name_prefix}-frontend-oac"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

resource "aws_cloudfront_distribution" "frontend" {
  enabled             = true
  is_ipv6_enabled     = true
  default_root_object = "index.html"
  comment             = "${var.app_name_prefix} frontend"
  price_class         = "PriceClass_100" # US + Europe — cheaper than All

  aliases = [var.domain_name, "www.${var.domain_name}"]

  origin {
    domain_name              = aws_s3_bucket.frontend.bucket_regional_domain_name
    origin_id                = "s3-frontend"
    origin_access_control_id = aws_cloudfront_origin_access_control.frontend.id
  }

  default_cache_behavior {
    target_origin_id       = "s3-frontend"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true

    forwarded_values {
      query_string = false
      cookies { forward = "none" }
    }

    min_ttl     = 0
    default_ttl = 3600
    max_ttl     = 86400
  }

  # SPA: 404s on dynamic routes are normal — return index.html so client-side routing works.
  custom_error_response {
    error_code         = 403
    response_code      = 200
    response_page_path = "/index.html"
  }
  custom_error_response {
    error_code         = 404
    response_code      = 200
    response_page_path = "/index.html"
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    acm_certificate_arn      = aws_acm_certificate_validation.frontend.certificate_arn
    ssl_support_method       = "sni-only"
    minimum_protocol_version = "TLSv1.2_2021"
  }
}

# S3 bucket policy: only CloudFront (via OAC) can read.
data "aws_iam_policy_document" "frontend_bucket" {
  statement {
    sid     = "AllowCloudFrontRead"
    actions = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.frontend.arn}/*"]
    principals {
      type        = "Service"
      identifiers = ["cloudfront.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [aws_cloudfront_distribution.frontend.arn]
    }
  }
}

resource "aws_s3_bucket_policy" "frontend" {
  bucket = aws_s3_bucket.frontend.id
  policy = data.aws_iam_policy_document.frontend_bucket.json
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/frontend.tf
git commit -m "feat(terraform/prod): S3 bucket + CloudFront distribution with OAC + SPA error responses"
```

---

### Task 16: Route53 records

**Files:**
- Create: `terraform/prod/route53.tf`

- [ ] **Step 1: Write `terraform/prod/route53.tf`**

```hcl
# api.example.com → ALB
resource "aws_route53_record" "api" {
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = "api.${var.domain_name}"
  type    = "A"
  alias {
    name                   = aws_lb.main.dns_name
    zone_id                = aws_lb.main.zone_id
    evaluate_target_health = true
  }
}

# example.com (apex) → CloudFront
resource "aws_route53_record" "apex" {
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = var.domain_name
  type    = "A"
  alias {
    name                   = aws_cloudfront_distribution.frontend.domain_name
    zone_id                = aws_cloudfront_distribution.frontend.hosted_zone_id
    evaluate_target_health = false
  }
}

# www.example.com → CloudFront (same distribution)
resource "aws_route53_record" "www" {
  zone_id = data.aws_route53_zone.primary.zone_id
  name    = "www.${var.domain_name}"
  type    = "A"
  alias {
    name                   = aws_cloudfront_distribution.frontend.domain_name
    zone_id                = aws_cloudfront_distribution.frontend.hosted_zone_id
    evaluate_target_health = false
  }
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/route53.tf
git commit -m "feat(terraform/prod): Route53 A records (api, apex, www)"
```

---

### Task 17: Outputs

**Files:**
- Create: `terraform/prod/outputs.tf`

- [ ] **Step 1: Write `terraform/prod/outputs.tf`**

```hcl
output "alb_dns_name" {
  description = "Direct ALB hostname (for diagnostics; users hit the Route53-fronted api.<domain>)."
  value       = aws_lb.main.dns_name
}

output "api_url" {
  description = "Public HTTPS API URL."
  value       = "https://api.${var.domain_name}"
}

output "frontend_url" {
  description = "Public HTTPS site URL."
  value       = "https://${var.domain_name}"
}

output "frontend_bucket" {
  description = "S3 bucket the frontend deploy script syncs to."
  value       = aws_s3_bucket.frontend.bucket
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID for the frontend deploy script's invalidation step."
  value       = aws_cloudfront_distribution.frontend.id
}

output "db_endpoint" {
  description = "RDS endpoint (host:port)."
  value       = aws_db_instance.main.endpoint
}

output "db_master_user_secret_arn" {
  description = "ARN of the RDS-managed master password secret."
  value       = aws_db_instance.main.master_user_secret[0].secret_arn
}

output "database_url_secret_arn" {
  description = "ARN of the secret holding the full DATABASE_URL DSN."
  value       = aws_secretsmanager_secret.database_url.arn
}

output "events_queue_url" {
  value = aws_sqs_queue.events.url
}

output "interests_queue_url" {
  value = aws_sqs_queue.interests.url
}

output "post_apply_steps" {
  description = "Operator checklist after first apply."
  value = <<-EOT
    1. Seed the Secrets Manager values that Terraform created as placeholders:
       for s in jwt-signing-key spotify-client-id spotify-client-secret spotify-token-enc-key ticketmaster-api-key; do
         aws secretsmanager put-secret-value \
           --secret-id "${var.app_name_prefix}/$s" \
           --secret-string "<real value>"
       done
       Note: spotify-token-enc-key must be 32 bytes base64-encoded (openssl rand -base64 32).

    2. Push a bootstrap image to ECR (only needed before the first ECS service deploy):
       aws ecr get-login-password --region ${var.aws_region} | docker login --username AWS \
         --password-stdin <account-id>.dkr.ecr.${var.aws_region}.amazonaws.com
       docker pull public.ecr.aws/nginx/nginx:latest
       docker tag public.ecr.aws/nginx/nginx:latest <account-id>.dkr.ecr.${var.aws_region}.amazonaws.com/${var.app_name_prefix}-app:bootstrap
       docker push <account-id>.dkr.ecr.${var.aws_region}.amazonaws.com/${var.app_name_prefix}-app:bootstrap

    3. Trigger the app pipeline (push to master, or manually start it in the AWS console).
       This builds the real Go image and rolls the api service + scheduled task defs.

    4. Run database migrations (one-time bootstrap from your laptop or a one-off ECS task):
       psql "${aws_secretsmanager_secret.database_url.name}" -f sql/migrations/*.up.sql
       (See README; production migrations should be a separate plan if frequent.)

    5. Configure the frontend deploy script with the outputs above:
       cat > web/.env.deploy <<EOF
       S3_BUCKET=${aws_s3_bucket.frontend.bucket}
       CLOUDFRONT_DISTRIBUTION_ID=${aws_cloudfront_distribution.frontend.id}
       VITE_API_BASE_URL=https://api.${var.domain_name}
       EOF
       cd web && pnpm deploy
  EOT
}
```

- [ ] **Step 2: Verify + commit**

```bash
cd terraform/prod
terraform fmt -check
terraform validate
git add terraform/prod/outputs.tf
git commit -m "feat(terraform/prod): outputs + post_apply_steps checklist"
```

---

### Task 18: Extend app pipeline buildspec to update scheduled task families

**File:** Modify `ci/buildspec-app.yml`

Plan 7's buildspec only updates the api service. For the scheduled task families to pick up new images, the buildspec needs to register a new task-def revision for each scheduled family. Since the EventBridge schedules reference task defs by family-only (no revision), they automatically pick up the new revision on the next firing.

- [ ] **Step 1: Read the current `ci/buildspec-app.yml`**

The Plan 7 file has a `build:` section whose `deploy` branch is a single shell block. We're extending that block to also update scheduled task families.

- [ ] **Step 2: Replace the `deploy` shell block**

In `ci/buildspec-app.yml`, find the part of the `build:` commands that starts with:
```bash
- |
  if [ "${APP_PHASE}" = "deploy" ]; then
    echo "=== Deploying ${IMAGE_URI} ==="
```

Replace the entire `if [ "${APP_PHASE}" = "deploy" ]` block (from that `if` line through the matching `fi`) with:

```bash
      - |
        if [ "${APP_PHASE}" = "deploy" ]; then
          echo "=== Deploying ${IMAGE_URI} ==="
          SERVICE_NAME="${APP_NAME_PREFIX}-api"
          CLUSTER_NAME="${APP_NAME_PREFIX}-cluster"
          # All task-def families managed by Plan 8.
          FAMILIES="${APP_NAME_PREFIX}-api ${APP_NAME_PREFIX}-scrape-events-ticketmaster ${APP_NAME_PREFIX}-scrape-spotify ${APP_NAME_PREFIX}-match"

          if aws ecs describe-services --cluster "${CLUSTER_NAME}" --services "${SERVICE_NAME}" --region "${AWS_REGION}" 2>/dev/null | grep -q '"status": "ACTIVE"'; then
            # Register a new revision per family, image swapped to the freshly-built one.
            for FAMILY in $FAMILIES; do
              echo "--- registering new revision for $FAMILY ---"
              CURRENT_ARN=$(aws ecs describe-task-definition --task-definition "$FAMILY" --region "${AWS_REGION}" --query 'taskDefinition.taskDefinitionArn' --output text)
              aws ecs describe-task-definition --task-definition "$CURRENT_ARN" --region "${AWS_REGION}" --query 'taskDefinition' > taskdef.json
              jq --arg img "${IMAGE_URI}" '
                .containerDefinitions[0].image = $img |
                del(.taskDefinitionArn, .revision, .status, .requiresAttributes, .compatibilities, .registeredAt, .registeredBy)
              ' taskdef.json > taskdef.new.json
              NEW_ARN=$(aws ecs register-task-definition --cli-input-json file://taskdef.new.json --region "${AWS_REGION}" --query 'taskDefinition.taskDefinitionArn' --output text)
              echo "registered $NEW_ARN"

              # api gets a rolling deploy via update-service. Others rely on EventBridge
              # picking up :LATEST next firing.
              if [ "$FAMILY" = "${APP_NAME_PREFIX}-api" ]; then
                aws ecs update-service --cluster "${CLUSTER_NAME}" --service "${SERVICE_NAME}" --task-definition "$NEW_ARN" --region "${AWS_REGION}" > /dev/null
                echo "deployed $NEW_ARN to $SERVICE_NAME"
              fi
            done
          else
            echo "Service ${SERVICE_NAME} not found in cluster ${CLUSTER_NAME} (Plan 8 not yet applied). Image is in ECR; skipping deploy."
          fi
        fi
```

- [ ] **Step 3: Verify YAML**

```bash
python3 -c "import yaml; yaml.safe_load(open('ci/buildspec-app.yml'))"
```

Expected: no output (success).

- [ ] **Step 4: Commit**

```bash
git add ci/buildspec-app.yml
git commit -m "feat(ci): extend app pipeline to register new revisions for scheduled task families"
```

---

### Task 19: README updates + final validation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Append Plan 8 quickstart at the end of README**

````markdown

## Plan 8 quickstart — Terraform prod infrastructure

This is the big one — the actual production runtime. Requires Plan 7 bootstrap
applied and the prerequisites listed in `docs/superpowers/plans/2026-05-26-plan-08-terraform-prod.md`.

### Prereqs (one-time)

1. Plan 7 bootstrap is applied (you already did this — Plan 7 outputs printed
   the state bucket name).
2. Register a domain. Create a Route53 public hosted zone for it. Update your
   registrar's nameservers to the four NS records in the zone. Wait for DNS
   propagation (`dig +short NS your-domain.com` returns the AWS nameservers).
3. Replace `REPLACE_WITH_ACCOUNT_ID` in `terraform/prod/backend.tf` with your
   AWS account ID (visible in any IAM resource ARN from Plan 7's outputs, or via
   `aws sts get-caller-identity --query Account --output text`).

### Apply

```bash
cd terraform/prod
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars and set domain_name to your real domain.
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

First apply takes ~15 minutes (RDS, CloudFront, ACM cert validation).

### Post-apply checklist

The outputs print a `post_apply_steps` heredoc — follow it:

1. **Seed Secrets Manager values** — Terraform creates the secret shells with
   placeholder values; you write the real secrets via `aws secretsmanager
   put-secret-value` for `jwt-signing-key`, `spotify-client-id`, etc.
2. **Push a bootstrap image to ECR** — needed before the first ECS service
   deploy. The output prints the exact docker commands.
3. **Trigger the app pipeline** — push a commit to master, or manually start
   the `hwh-app-pipeline` in the AWS console. This builds the real Go image
   and rolls the api service.
4. **Run database migrations** — connect to RDS using the master credentials
   from Secrets Manager, run all migrations under `sql/migrations/`. A future
   plan should automate this; for v1 it's a one-time setup.
5. **Deploy the frontend** — fill in `web/.env.deploy` with the bucket name +
   distribution ID from the outputs, then `cd web && pnpm deploy`.

After all five: hit `https://api.your-domain.com/healthz` (should return
`{"status":"ok"}`) and load `https://your-domain.com` in a browser.
````

- [ ] **Step 2: Final validation pass**

```bash
cd terraform/prod
terraform fmt -check -recursive
terraform validate
```

Expected: no formatting issues, `Success! The configuration is valid.`

- [ ] **Step 3: Commit**

```bash
cd /Users/wmyers/data/heres-whats-happening
git add README.md
git commit -m "docs: Plan 8 Terraform prod quickstart"
```

---

## Self-Review

**Spec coverage check (Plan 8 scope — design.md §11 "Infrastructure"):**

| Spec requirement | Implemented in |
|---|---|
| VPC with public + private subnets across 2 AZs | Task 2 |
| ALB in public, ECS + RDS in private | Tasks 2, 3, 8, 12, 13 |
| Single NAT gateway | Task 2 |
| ECS Fargate cluster | Task 9 |
| api service behind ALB on HTTPS | Tasks 8, 12 |
| tei sidecar internal-only with Cloud Map | Tasks 9, 13 |
| EventBridge Scheduler rules per source | Task 14 |
| event-scraper / spotify-scraper / match-job schedules at 00:00 / 00:00 / 02:00 | Task 14 |
| SQS events-queue + events-dlq, interests-queue + interests-dlq, redrive 3 | Task 6 |
| DLQ depth CloudWatch alarms | Task 6 |
| RDS PostgreSQL 16 + pgvector (db.t4g.small) | Task 5 |
| Secrets Manager: JWT, DB password, Spotify, encryption key | Tasks 4, 5 |
| IAM task roles with least-privilege access per task | Task 10 |
| ECR (from Plan 7) | Task 1 (data lookup) |
| CloudWatch Logs 30-day retention | Task 11 |
| S3 + CloudFront frontend | Task 15 |
| Route53 records (apex + www + api) | Task 16 |
| ACM certs for both example.com and api.example.com | Task 7 |
| Frontend → CloudFront default behavior returns index.html on 404 | Task 15 (custom_error_response 403+404 → 200 + /index.html) |
| App pipeline updates scheduled task families | Task 18 |

**Deferred to future plans (per spec or for v1 simplicity):**

- Multi-AZ RDS (spec says off for v1; turn on before launch)
- Automated migrations (one-time manual in v1; future plan should add a migrate task triggered by the pipeline)
- VPC endpoints for Secrets Manager + ECR (avoid NAT egress for those; future cost-saving)
- WAF on the ALB (security upgrade)
- WAF on CloudFront (same)
- Service auto-scaling on the api service
- TEI on GPU (cost vs. throughput tradeoff for higher scale)
- Real `route53_zone` creation (currently a data lookup — user creates the zone manually)
- `terraform.tfvars` with sensitive values (the file is gitignored; user fills in spotify_client_secret etc. by hand)

**Placeholder scan:** no "TBD"/"add error handling"/"handle edge cases" steps. Every code-touching step has complete code. The literal "REPLACE_WITH_ACCOUNT_ID" in `backend.tf` (Task 1) is documented as a one-time developer fix-up, not a plan placeholder.

**Type consistency:**

- `${var.app_name_prefix}-` interpolation is consistent across all resource names (`hwh-vpc`, `hwh-alb`, `hwh-api`, `hwh-cluster`, etc.).
- `data.aws_ecr_repository.app.repository_url` referenced consistently across Tasks 12 + 14.
- `aws_security_group.api_task`, `aws_security_group.tei_task`, `aws_security_group.task_runner`, `aws_security_group.rds` — names consistent across iam.tf, ecs_api.tf, ecs_tei.tf, ecs_schedules.tf, rds.tf.
- `aws_subnet.private[*].id` and `aws_subnet.public[*].id` consistent.
- `local.api_env_vars` + `local.api_secrets` in `ecs_api.tf` (Task 12) reused as `local.scheduled_env_vars` + `local.scheduled_secrets` in `ecs_schedules.tf` (Task 14).
- `aws_secretsmanager_secret.app["jwt-signing-key"].arn` style is consistent (for_each map indexing).
- Plan 7's `${APP_NAME_PREFIX}-api` service name pattern matches Plan 8's `aws_ecs_service.api` name (`"${var.app_name_prefix}-api"`). The pipeline's hardcoded `SERVICE_NAME="${APP_NAME_PREFIX}-api"` lines up.

**Plan-internal consistency notes:**

- The `lifecycle.ignore_changes = [container_definitions]` on `aws_ecs_task_definition.api` (Task 12) and `aws_ecs_task_definition.scheduled` (Task 14) prevents Terraform from fighting the app pipeline's image swaps. The TEI task def (Task 13) does NOT have this lifecycle hook because TEI's image is Terraform-managed (var.tei_image).
- The api task def's `local.api_image` initially points at `:bootstrap`. The user pushes a placeholder image with that tag before applying Plan 8 (post_apply_steps step 2). The next app pipeline run replaces the image with a real SHA-tagged build.
- DATABASE_URL is constructed in Terraform from the RDS-managed master password (via a `data` lookup on the RDS-managed secret) and written to a separate Secrets Manager entry. ECS pulls the full DSN as a single env var. This avoids needing to refactor the Go app's config layer.
- The CORS_ALLOWED_ORIGINS env var (Task 12 `api_env_vars`) is set to `https://<domain>,https://www.<domain>` so the SPA on both apex and www can call the API.
- `SPOTIFY_REDIRECT_URI` is `https://api.<domain>/integrations/spotify/callback`. The user must register this exact URL in the Spotify developer dashboard.
- The buildspec extension in Task 18 assumes the api family is named `${APP_NAME_PREFIX}-api` and scheduled families follow the `${APP_NAME_PREFIX}-{scrape-events-ticketmaster|scrape-spotify|match}` pattern. Plan 8's task names (`aws_ecs_task_definition.scheduled[each.key].family`) match.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-26-plan-08-terraform-prod.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
