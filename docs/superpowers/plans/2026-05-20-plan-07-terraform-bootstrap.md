# Plan 7 — Terraform Bootstrap + CI/CD Pipelines Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Author the `terraform/bootstrap/` stack (and supporting CI buildspecs) that, when applied once by the developer with their AWS credentials, creates the S3 + DynamoDB state backend for the `prod` stack, the GitHub CodeStar connection, the ECR repo, an SNS approval topic, IAM roles, and two CodePipelines + CodeBuild projects (one pair for Terraform infra plan/apply, one pair for app build+deploy).

**Architecture:** Bootstrap is its own Terraform stack with LOCAL state (gitignored; developer keeps the file safe). It depends on no other stack and is the one-time setup. All resources go in a single root module under `terraform/bootstrap/`, split by responsibility into per-file resource groupings. The actual `terraform apply` is run by the user; CI does not execute Terraform until *after* the pipelines exist (chicken-and-egg). After bootstrap, future changes to `terraform/prod/` (built in Plan 8) flow through the infra pipeline; future changes to `cmd/app/`, `internal/`, `Dockerfile`, etc. flow through the app pipeline.

**Tech Stack:** Terraform 1.7.x · AWS provider ~> 5.0 · CodePipeline + CodeBuild + CodeStar Connections + S3 + DynamoDB + ECR + SNS + IAM · YAML buildspecs.

---

## File Structure

```
.
├── terraform/
│   ├── .terraform-version                          # tfenv hint: 1.7.5
│   └── bootstrap/
│       ├── versions.tf                             # required_version + provider pins
│       ├── variables.tf                            # aws_region, github_repo, app_name_prefix
│       ├── providers.tf                            # aws provider + default_tags
│       ├── data.tf                                 # data.aws_caller_identity for account_id
│       ├── state.tf                                # S3 state bucket + DynamoDB lock table
│       ├── github.tf                               # CodeStar Connection (PENDING after apply)
│       ├── ecr.tf                                  # hwh-app ECR repository
│       ├── sns.tf                                  # hwh-infra-approval topic + email subscription
│       ├── iam.tf                                  # 3 service roles + their policies
│       ├── codebuild.tf                            # 4 CodeBuild projects
│       ├── pipelines.tf                            # infra + app CodePipelines
│       └── outputs.tf                              # backend bucket name, ECR URL, etc.
├── ci/
│   ├── buildspec-infra-plan.yml                    # terraform init + plan, upload tfplan
│   ├── buildspec-infra-apply.yml                   # terraform apply against the saved plan
│   └── buildspec-app.yml                           # go test, docker build/push, deploy hook
└── .gitignore                                      # add terraform/bootstrap/{terraform.tfstate*,.terraform/}
```

**Boundaries:**

- Each .tf file owns one logical chunk of resources. Splits make navigation easier; Terraform itself merges all .tf files in a directory anyway.
- The `ci/` directory holds buildspecs that are checked into the repo and referenced by the CodeBuild projects' source configuration. They get executed by CodeBuild at pipeline runtime — not by Terraform.
- Bootstrap state is LOCAL. The S3/DynamoDB backend bootstrap creates is for the *prod* stack (Plan 8), not for itself.

**Naming conventions used throughout:**
- Resource name prefix: `hwh-` (matches existing container names like `hwh_postgres`)
- App name: `hwh-app`
- Pipeline names: `hwh-infra-pipeline`, `hwh-app-pipeline`
- CodeBuild project names: `hwh-infra-plan`, `hwh-infra-apply`, `hwh-app-build`, `hwh-app-deploy`
- S3 state bucket: `hwh-tf-state-<account-id>` (account-id suffix for global uniqueness)
- DynamoDB lock table: `hwh-tf-state-lock`
- SNS approval topic: `hwh-infra-approval`

---

## Prerequisites

Before running `terraform apply` in Task 13's smoke-test, the developer needs:

- An AWS account with admin (or sufficient) credentials configured (`aws sts get-caller-identity` succeeds).
- Terraform 1.7.x installed (`tfenv install 1.7.5` or via Homebrew).
- A GitHub account that will authorize the CodeStar Connection.
- An email address for the SNS subscription (manual-approval notifications).

Plan 7's task work itself doesn't need AWS credentials — `terraform fmt` and `terraform validate` are offline checks. The final smoke test (apply) is run by the developer.

---

### Task 1: Terraform directory structure + version pin + gitignore

**Files:**
- Create: `terraform/.terraform-version`
- Create: `terraform/bootstrap/.gitignore`
- Modify: root `.gitignore`

- [ ] **Step 1: Create the directory and version-pin file**

```bash
mkdir -p terraform/bootstrap
echo "1.7.5" > terraform/.terraform-version
```

- [ ] **Step 2: Create `terraform/bootstrap/.gitignore`**

```
# Local Terraform state (bootstrap uses local backend)
terraform.tfstate
terraform.tfstate.backup
terraform.tfstate.*.backup

# Terraform working dir (cached providers + modules)
.terraform/
.terraform.lock.hcl

# Plan files
*.tfplan
```

Note: `.terraform.lock.hcl` is normally committed for reproducibility, but for bootstrap (run once and rarely) the simpler choice is to ignore it. The developer can regenerate it via `terraform init`.

- [ ] **Step 3: Extend root `.gitignore`**

Append to the existing `.gitignore`:

```
# Terraform secrets
*.tfvars
!*.tfvars.example
```

This lets us check in `*.tfvars.example` (template) while keeping the real `*.tfvars` (might contain secrets) out of git.

- [ ] **Step 4: Verify**

```bash
ls -la terraform/
cat terraform/.terraform-version
cat terraform/bootstrap/.gitignore
```

- [ ] **Step 5: Commit**

```bash
git add terraform/.terraform-version terraform/bootstrap/.gitignore .gitignore
git commit -m "feat(terraform): bootstrap directory scaffold + gitignores"
```

---

### Task 2: Foundation — `versions.tf`, `variables.tf`, `providers.tf`

**Files:**
- Create: `terraform/bootstrap/versions.tf`
- Create: `terraform/bootstrap/variables.tf`
- Create: `terraform/bootstrap/providers.tf`
- Create: `terraform/bootstrap/data.tf`
- Create: `terraform/bootstrap/terraform.tfvars.example`

- [ ] **Step 1: Write `terraform/bootstrap/versions.tf`**

```hcl
terraform {
  required_version = "~> 1.7"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
```

- [ ] **Step 2: Write `terraform/bootstrap/variables.tf`**

```hcl
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
```

- [ ] **Step 3: Write `terraform/bootstrap/providers.tf`**

```hcl
provider "aws" {
  region = var.aws_region
  default_tags {
    tags = {
      Project   = "heres-whats-happening"
      Stack     = "bootstrap"
      ManagedBy = "terraform"
    }
  }
}
```

- [ ] **Step 4: Write `terraform/bootstrap/data.tf`**

```hcl
# Account ID is used to make globally unique resource names (e.g., S3 bucket).
data "aws_caller_identity" "current" {}

data "aws_region" "current" {}
```

- [ ] **Step 5: Write `terraform/bootstrap/terraform.tfvars.example`**

```hcl
# Copy to terraform.tfvars and fill in.
# Required:
approval_email = "you@example.com"

# Optional overrides:
# aws_region      = "us-east-1"
# app_name_prefix = "hwh"
# github_owner    = "wmyers"
# github_repo     = "heres-whats-happening"
# github_branch   = "master"
```

- [ ] **Step 6: Verify**

```bash
cd terraform/bootstrap
terraform fmt -check
terraform init -backend=false   # downloads providers; -backend=false skips local backend init
terraform validate
```

Expected: `Success! The configuration is valid.`

- [ ] **Step 7: Commit**

```bash
git add terraform/bootstrap/versions.tf terraform/bootstrap/variables.tf terraform/bootstrap/providers.tf terraform/bootstrap/data.tf terraform/bootstrap/terraform.tfvars.example
git commit -m "feat(terraform): bootstrap foundation — versions, variables, provider, data"
```

---

### Task 3: State backend — S3 bucket + DynamoDB lock table

**Files:**
- Create: `terraform/bootstrap/state.tf`

- [ ] **Step 1: Write `terraform/bootstrap/state.tf`**

```hcl
# S3 bucket for the prod stack's remote Terraform state.
# Naming: <prefix>-tf-state-<account-id> for global uniqueness.

locals {
  state_bucket_name = "${var.app_name_prefix}-tf-state-${data.aws_caller_identity.current.account_id}"
}

resource "aws_s3_bucket" "tf_state" {
  bucket = local.state_bucket_name

  tags = {
    Purpose = "terraform-state-backend"
  }
}

resource "aws_s3_bucket_versioning" "tf_state" {
  bucket = aws_s3_bucket.tf_state.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "tf_state" {
  bucket = aws_s3_bucket.tf_state.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "tf_state" {
  bucket                  = aws_s3_bucket.tf_state.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_dynamodb_table" "tf_state_lock" {
  name         = "${var.app_name_prefix}-tf-state-lock"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }

  tags = {
    Purpose = "terraform-state-locking"
  }
}
```

- [ ] **Step 2: Verify**

```bash
cd terraform/bootstrap
terraform fmt -check
terraform validate
```

Expected: `Success!`.

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/state.tf
git commit -m "feat(terraform): S3 bucket + DynamoDB lock table for prod state backend"
```

---

### Task 4: GitHub CodeStar Connection

**Files:**
- Create: `terraform/bootstrap/github.tf`

- [ ] **Step 1: Write `terraform/bootstrap/github.tf`**

```hcl
# CodeStar Connection to GitHub. After `terraform apply` the connection is
# created in PENDING state — finalize it via:
#   AWS Console → CodePipeline → Settings → Connections → click the connection
#   → "Update pending connection" → authorize the AWS app on GitHub.
# Until that one-time step happens, the pipelines fail at the Source stage.

resource "aws_codestarconnections_connection" "github" {
  name          = "${var.app_name_prefix}-github"
  provider_type = "GitHub"
}
```

- [ ] **Step 2: Verify**

```bash
cd terraform/bootstrap
terraform fmt -check
terraform validate
```

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/github.tf
git commit -m "feat(terraform): GitHub CodeStar Connection"
```

---

### Task 5: ECR repository

**Files:**
- Create: `terraform/bootstrap/ecr.tf`

- [ ] **Step 1: Write `terraform/bootstrap/ecr.tf`**

```hcl
# ECR repository for the Go app's Docker image (single image, multi-subcommand
# pattern from Plan 1 spec).

resource "aws_ecr_repository" "app" {
  name                 = "${var.app_name_prefix}-app"
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }
}

# Lifecycle policy: keep last 30 images, expire older untagged images after 14 days.
resource "aws_ecr_lifecycle_policy" "app" {
  repository = aws_ecr_repository.app.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 30 tagged images"
        selection = {
          tagStatus     = "tagged"
          tagPatternList = ["*"]
          countType     = "imageCountMoreThan"
          countNumber   = 30
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Expire untagged images after 14 days"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 14
        }
        action = { type = "expire" }
      }
    ]
  })
}
```

- [ ] **Step 2: Verify**

```bash
cd terraform/bootstrap
terraform fmt -check
terraform validate
```

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/ecr.tf
git commit -m "feat(terraform): ECR repository with lifecycle policy"
```

---

### Task 6: SNS approval topic + email subscription

**Files:**
- Create: `terraform/bootstrap/sns.tf`

- [ ] **Step 1: Write `terraform/bootstrap/sns.tf`**

```hcl
# SNS topic that the infra pipeline's manual-approval action publishes to.
# The email subscription must be confirmed via the link AWS sends after apply.

resource "aws_sns_topic" "infra_approval" {
  name = "${var.app_name_prefix}-infra-approval"
}

resource "aws_sns_topic_subscription" "infra_approval_email" {
  topic_arn = aws_sns_topic.infra_approval.arn
  protocol  = "email"
  endpoint  = var.approval_email
}
```

- [ ] **Step 2: Verify**

```bash
cd terraform/bootstrap
terraform fmt -check
terraform validate
```

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/sns.tf
git commit -m "feat(terraform): SNS approval topic + email subscription"
```

---

### Task 7: IAM roles

**Files:**
- Create: `terraform/bootstrap/iam.tf`

Three service roles:
1. `codepipeline_service` — used by both CodePipelines
2. `codebuild_infra` — used by `hwh-infra-plan` and `hwh-infra-apply` (needs broad AWS access since terraform creates many resources)
3. `codebuild_app` — used by `hwh-app-build` and `hwh-app-deploy` (only ECR + ECS + Secrets Manager read)

- [ ] **Step 1: Write `terraform/bootstrap/iam.tf`**

```hcl
# ---------------------------------------------------------------------------
# CodePipeline service role
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "codepipeline_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["codepipeline.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "codepipeline_service" {
  name               = "${var.app_name_prefix}-codepipeline-service"
  assume_role_policy = data.aws_iam_policy_document.codepipeline_assume.json
}

data "aws_iam_policy_document" "codepipeline_service" {
  # Read+write the S3 artifact bucket (created below).
  statement {
    actions = [
      "s3:GetObject",
      "s3:GetObjectVersion",
      "s3:PutObject",
      "s3:GetBucketVersioning",
    ]
    resources = [
      aws_s3_bucket.pipeline_artifacts.arn,
      "${aws_s3_bucket.pipeline_artifacts.arn}/*",
    ]
  }
  # Start CodeBuild projects.
  statement {
    actions   = ["codebuild:BatchGetBuilds", "codebuild:StartBuild"]
    resources = ["*"]
  }
  # Use the CodeStar Connection.
  statement {
    actions   = ["codestar-connections:UseConnection"]
    resources = [aws_codestarconnections_connection.github.arn]
  }
  # Publish to the approval topic.
  statement {
    actions   = ["sns:Publish"]
    resources = [aws_sns_topic.infra_approval.arn]
  }
}

resource "aws_iam_role_policy" "codepipeline_service" {
  role   = aws_iam_role.codepipeline_service.id
  policy = data.aws_iam_policy_document.codepipeline_service.json
}

# ---------------------------------------------------------------------------
# Pipeline artifacts bucket (used by both pipelines to pass artifacts between stages)
# ---------------------------------------------------------------------------

resource "aws_s3_bucket" "pipeline_artifacts" {
  bucket        = "${var.app_name_prefix}-pipeline-artifacts-${data.aws_caller_identity.current.account_id}"
  force_destroy = true
}

resource "aws_s3_bucket_versioning" "pipeline_artifacts" {
  bucket = aws_s3_bucket.pipeline_artifacts.id
  versioning_configuration { status = "Enabled" }
}

resource "aws_s3_bucket_public_access_block" "pipeline_artifacts" {
  bucket                  = aws_s3_bucket.pipeline_artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# ---------------------------------------------------------------------------
# CodeBuild assume-role policy (shared by both build roles)
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "codebuild_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["codebuild.amazonaws.com"]
    }
  }
}

# ---------------------------------------------------------------------------
# CodeBuild role for terraform-running projects (broad AWS permissions)
# ---------------------------------------------------------------------------

resource "aws_iam_role" "codebuild_infra" {
  name               = "${var.app_name_prefix}-codebuild-infra"
  assume_role_policy = data.aws_iam_policy_document.codebuild_assume.json
}

# Terraform creates many resource types. For v1 we grant a broad
# AdministratorAccess; tighten later by enumerating the actually-needed
# IAM/EC2/RDS/ECS/etc. statements.
resource "aws_iam_role_policy_attachment" "codebuild_infra_admin" {
  role       = aws_iam_role.codebuild_infra.name
  policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
}

# Additionally needed: read/write the artifacts bucket + write CloudWatch Logs.
data "aws_iam_policy_document" "codebuild_infra_inline" {
  statement {
    actions = ["s3:GetObject", "s3:GetObjectVersion", "s3:PutObject"]
    resources = [
      aws_s3_bucket.pipeline_artifacts.arn,
      "${aws_s3_bucket.pipeline_artifacts.arn}/*",
    ]
  }
  statement {
    actions = [
      "logs:CreateLogGroup",
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "codebuild_infra_inline" {
  role   = aws_iam_role.codebuild_infra.id
  policy = data.aws_iam_policy_document.codebuild_infra_inline.json
}

# ---------------------------------------------------------------------------
# CodeBuild role for the app build+deploy projects (narrow permissions)
# ---------------------------------------------------------------------------

resource "aws_iam_role" "codebuild_app" {
  name               = "${var.app_name_prefix}-codebuild-app"
  assume_role_policy = data.aws_iam_policy_document.codebuild_assume.json
}

data "aws_iam_policy_document" "codebuild_app" {
  # ECR push + pull.
  statement {
    actions = [
      "ecr:GetAuthorizationToken",
    ]
    resources = ["*"]
  }
  statement {
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:GetDownloadUrlForLayer",
      "ecr:BatchGetImage",
      "ecr:PutImage",
      "ecr:InitiateLayerUpload",
      "ecr:UploadLayerPart",
      "ecr:CompleteLayerUpload",
    ]
    resources = [aws_ecr_repository.app.arn]
  }
  # ECS deploy actions (the resources don't exist yet — Plan 8 creates them —
  # but the role permits future operations against any ECS service in this account).
  statement {
    actions = [
      "ecs:RegisterTaskDefinition",
      "ecs:DescribeTaskDefinition",
      "ecs:UpdateService",
      "ecs:DescribeServices",
      "ecs:DescribeTasks",
      "ecs:ListTasks",
    ]
    resources = ["*"]
  }
  # Pass roles to ECS tasks (Plan 8 will create task execution + task roles).
  statement {
    actions   = ["iam:PassRole"]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }
  # Update EventBridge Scheduler targets so scheduled tasks pick up new task-def revisions.
  statement {
    actions = [
      "scheduler:GetSchedule",
      "scheduler:UpdateSchedule",
      "scheduler:ListSchedules",
    ]
    resources = ["*"]
  }
  # Artifact bucket + logs.
  statement {
    actions = ["s3:GetObject", "s3:GetObjectVersion", "s3:PutObject"]
    resources = [
      aws_s3_bucket.pipeline_artifacts.arn,
      "${aws_s3_bucket.pipeline_artifacts.arn}/*",
    ]
  }
  statement {
    actions = [
      "logs:CreateLogGroup",
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "codebuild_app" {
  role   = aws_iam_role.codebuild_app.id
  policy = data.aws_iam_policy_document.codebuild_app.json
}
```

- [ ] **Step 2: Verify**

```bash
cd terraform/bootstrap
terraform fmt -check
terraform validate
```

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/iam.tf
git commit -m "feat(terraform): IAM roles for CodePipeline + CodeBuild + artifacts bucket"
```

---

### Task 8: CodeBuild projects

**Files:**
- Create: `terraform/bootstrap/codebuild.tf`

Four CodeBuild projects:
- `hwh-infra-plan` — runs `ci/buildspec-infra-plan.yml`
- `hwh-infra-apply` — runs `ci/buildspec-infra-apply.yml`
- `hwh-app-build` — runs `ci/buildspec-app.yml` (build phase)
- `hwh-app-deploy` — runs `ci/buildspec-app.yml` (deploy phase) — same buildspec, different env var to control which phase to execute

For simplicity, we'll point the app-build and app-deploy projects at the same buildspec but pass an `APP_PHASE` env var to gate which steps each phase runs.

- [ ] **Step 1: Write `terraform/bootstrap/codebuild.tf`**

```hcl
# ---------------------------------------------------------------------------
# Shared environment configuration
# ---------------------------------------------------------------------------

locals {
  # CodeBuild source is CODEPIPELINE — the actual GitHub fetch happens at the
  # pipeline Source stage, and CodeBuild reads its input from the pipeline.
  cb_compute_type = "BUILD_GENERAL1_SMALL"
  cb_image        = "aws/codebuild/standard:7.0"
}

# ---------------------------------------------------------------------------
# Terraform infra: plan
# ---------------------------------------------------------------------------

resource "aws_codebuild_project" "infra_plan" {
  name          = "${var.app_name_prefix}-infra-plan"
  service_role  = aws_iam_role.codebuild_infra.arn
  build_timeout = 30

  artifacts {
    type = "CODEPIPELINE"
  }

  environment {
    compute_type    = local.cb_compute_type
    image           = local.cb_image
    type            = "LINUX_CONTAINER"
    privileged_mode = false

    environment_variable {
      name  = "AWS_REGION"
      value = var.aws_region
    }
    environment_variable {
      name  = "TF_STACK_DIR"
      value = "terraform/prod"
    }
  }

  source {
    type      = "CODEPIPELINE"
    buildspec = "ci/buildspec-infra-plan.yml"
  }

  logs_config {
    cloudwatch_logs {
      group_name = "/aws/codebuild/${var.app_name_prefix}-infra-plan"
    }
  }
}

# ---------------------------------------------------------------------------
# Terraform infra: apply (consumes the plan artifact)
# ---------------------------------------------------------------------------

resource "aws_codebuild_project" "infra_apply" {
  name          = "${var.app_name_prefix}-infra-apply"
  service_role  = aws_iam_role.codebuild_infra.arn
  build_timeout = 60

  artifacts {
    type = "CODEPIPELINE"
  }

  environment {
    compute_type    = local.cb_compute_type
    image           = local.cb_image
    type            = "LINUX_CONTAINER"
    privileged_mode = false

    environment_variable {
      name  = "AWS_REGION"
      value = var.aws_region
    }
    environment_variable {
      name  = "TF_STACK_DIR"
      value = "terraform/prod"
    }
  }

  source {
    type      = "CODEPIPELINE"
    buildspec = "ci/buildspec-infra-apply.yml"
  }

  logs_config {
    cloudwatch_logs {
      group_name = "/aws/codebuild/${var.app_name_prefix}-infra-apply"
    }
  }
}

# ---------------------------------------------------------------------------
# App: build (test + Docker build + push to ECR)
# ---------------------------------------------------------------------------

resource "aws_codebuild_project" "app_build" {
  name          = "${var.app_name_prefix}-app-build"
  service_role  = aws_iam_role.codebuild_app.arn
  build_timeout = 30

  artifacts {
    type = "CODEPIPELINE"
  }

  environment {
    compute_type    = local.cb_compute_type
    image           = local.cb_image
    type            = "LINUX_CONTAINER"
    privileged_mode = true # required for docker build

    environment_variable {
      name  = "AWS_REGION"
      value = var.aws_region
    }
    environment_variable {
      name  = "AWS_ACCOUNT_ID"
      value = data.aws_caller_identity.current.account_id
    }
    environment_variable {
      name  = "ECR_REPO"
      value = aws_ecr_repository.app.name
    }
    environment_variable {
      name  = "APP_PHASE"
      value = "build"
    }
  }

  source {
    type      = "CODEPIPELINE"
    buildspec = "ci/buildspec-app.yml"
  }

  logs_config {
    cloudwatch_logs {
      group_name = "/aws/codebuild/${var.app_name_prefix}-app-build"
    }
  }
}

# ---------------------------------------------------------------------------
# App: deploy (update ECS task defs + services + EventBridge schedules)
# ---------------------------------------------------------------------------

resource "aws_codebuild_project" "app_deploy" {
  name          = "${var.app_name_prefix}-app-deploy"
  service_role  = aws_iam_role.codebuild_app.arn
  build_timeout = 30

  artifacts {
    type = "CODEPIPELINE"
  }

  environment {
    compute_type    = local.cb_compute_type
    image           = local.cb_image
    type            = "LINUX_CONTAINER"
    privileged_mode = false

    environment_variable {
      name  = "AWS_REGION"
      value = var.aws_region
    }
    environment_variable {
      name  = "AWS_ACCOUNT_ID"
      value = data.aws_caller_identity.current.account_id
    }
    environment_variable {
      name  = "ECR_REPO"
      value = aws_ecr_repository.app.name
    }
    environment_variable {
      name  = "APP_PHASE"
      value = "deploy"
    }
    environment_variable {
      name  = "APP_NAME_PREFIX"
      value = var.app_name_prefix
    }
  }

  source {
    type      = "CODEPIPELINE"
    buildspec = "ci/buildspec-app.yml"
  }

  logs_config {
    cloudwatch_logs {
      group_name = "/aws/codebuild/${var.app_name_prefix}-app-deploy"
    }
  }
}
```

- [ ] **Step 2: Verify**

```bash
cd terraform/bootstrap
terraform fmt -check
terraform validate
```

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/codebuild.tf
git commit -m "feat(terraform): 4 CodeBuild projects (infra-plan, infra-apply, app-build, app-deploy)"
```

---

### Task 9: CodePipelines

**Files:**
- Create: `terraform/bootstrap/pipelines.tf`

Two pipelines, each Source → Build (→ Approval → Apply for infra) → Deploy (for app).

- [ ] **Step 1: Write `terraform/bootstrap/pipelines.tf`**

```hcl
# ---------------------------------------------------------------------------
# Infra pipeline: Source → Plan → Manual Approval → Apply
# ---------------------------------------------------------------------------

resource "aws_codepipeline" "infra" {
  name     = "${var.app_name_prefix}-infra-pipeline"
  role_arn = aws_iam_role.codepipeline_service.arn

  artifact_store {
    type     = "S3"
    location = aws_s3_bucket.pipeline_artifacts.bucket
  }

  stage {
    name = "Source"
    action {
      name             = "Source"
      category         = "Source"
      owner            = "AWS"
      provider         = "CodeStarSourceConnection"
      version          = "1"
      output_artifacts = ["source_output"]

      configuration = {
        ConnectionArn    = aws_codestarconnections_connection.github.arn
        FullRepositoryId = "${var.github_owner}/${var.github_repo}"
        BranchName       = var.github_branch
        DetectChanges    = "true"
      }
    }
  }

  stage {
    name = "Plan"
    action {
      name             = "TerraformPlan"
      category         = "Build"
      owner            = "AWS"
      provider         = "CodeBuild"
      version          = "1"
      input_artifacts  = ["source_output"]
      output_artifacts = ["plan_output"]

      configuration = {
        ProjectName = aws_codebuild_project.infra_plan.name
      }
    }
  }

  stage {
    name = "Approval"
    action {
      name     = "ManualApproval"
      category = "Approval"
      owner    = "AWS"
      provider = "Manual"
      version  = "1"

      configuration = {
        NotificationArn = aws_sns_topic.infra_approval.arn
        CustomData      = "Review the terraform plan in CloudWatch Logs (project ${aws_codebuild_project.infra_plan.name}), then approve or reject."
      }
    }
  }

  stage {
    name = "Apply"
    action {
      name            = "TerraformApply"
      category        = "Build"
      owner           = "AWS"
      provider        = "CodeBuild"
      version         = "1"
      input_artifacts = ["plan_output"]

      configuration = {
        ProjectName = aws_codebuild_project.infra_apply.name
      }
    }
  }
}

# ---------------------------------------------------------------------------
# App pipeline: Source → Build → Deploy
# ---------------------------------------------------------------------------

resource "aws_codepipeline" "app" {
  name     = "${var.app_name_prefix}-app-pipeline"
  role_arn = aws_iam_role.codepipeline_service.arn

  artifact_store {
    type     = "S3"
    location = aws_s3_bucket.pipeline_artifacts.bucket
  }

  stage {
    name = "Source"
    action {
      name             = "Source"
      category         = "Source"
      owner            = "AWS"
      provider         = "CodeStarSourceConnection"
      version          = "1"
      output_artifacts = ["source_output"]

      configuration = {
        ConnectionArn    = aws_codestarconnections_connection.github.arn
        FullRepositoryId = "${var.github_owner}/${var.github_repo}"
        BranchName       = var.github_branch
        DetectChanges    = "true"
      }
    }
  }

  stage {
    name = "Build"
    action {
      name             = "BuildAndPush"
      category         = "Build"
      owner            = "AWS"
      provider         = "CodeBuild"
      version          = "1"
      input_artifacts  = ["source_output"]
      output_artifacts = ["build_output"]

      configuration = {
        ProjectName = aws_codebuild_project.app_build.name
      }
    }
  }

  stage {
    name = "Deploy"
    action {
      name            = "DeployECS"
      category        = "Build"
      owner           = "AWS"
      provider        = "CodeBuild"
      version         = "1"
      input_artifacts = ["build_output"]

      configuration = {
        ProjectName = aws_codebuild_project.app_deploy.name
      }
    }
  }
}
```

- [ ] **Step 2: Verify**

```bash
cd terraform/bootstrap
terraform fmt -check
terraform validate
```

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/pipelines.tf
git commit -m "feat(terraform): infra + app CodePipelines"
```

---

### Task 10: Outputs

**Files:**
- Create: `terraform/bootstrap/outputs.tf`

- [ ] **Step 1: Write `terraform/bootstrap/outputs.tf`**

```hcl
output "tf_state_bucket" {
  description = "S3 bucket name for the prod stack's remote state."
  value       = aws_s3_bucket.tf_state.bucket
}

output "tf_state_lock_table" {
  description = "DynamoDB table for prod state locking."
  value       = aws_dynamodb_table.tf_state_lock.name
}

output "ecr_repository_url" {
  description = "Docker pull/push URL for the app image."
  value       = aws_ecr_repository.app.repository_url
}

output "github_connection_arn" {
  description = "CodeStar Connection ARN. Must be authorized in the AWS console after apply."
  value       = aws_codestarconnections_connection.github.arn
}

output "infra_pipeline_name" {
  value = aws_codepipeline.infra.name
}

output "app_pipeline_name" {
  value = aws_codepipeline.app.name
}

output "approval_topic_arn" {
  description = "SNS topic that emits manual-approval notifications. Email subscription must be confirmed."
  value       = aws_sns_topic.infra_approval.arn
}

output "pipeline_artifacts_bucket" {
  description = "S3 bucket the pipelines use to pass artifacts between stages."
  value       = aws_s3_bucket.pipeline_artifacts.bucket
}

output "post_apply_steps" {
  description = "Human-readable next-steps after bootstrap apply."
  value       = <<-EOT
    1. Authorize the GitHub CodeStar Connection:
       AWS Console → Developer Tools → Settings → Connections →
       ${aws_codestarconnections_connection.github.name} → Update pending connection.
    2. Confirm the SNS email subscription sent to ${var.approval_email}.
    3. Once Plan 8 (terraform/prod) is written, push to master; the infra
       pipeline auto-triggers and waits for your manual approval.
  EOT
}
```

- [ ] **Step 2: Verify**

```bash
cd terraform/bootstrap
terraform fmt -check
terraform validate
```

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/outputs.tf
git commit -m "feat(terraform): bootstrap outputs"
```

---

### Task 11: CI buildspecs

**Files:**
- Create: `ci/buildspec-infra-plan.yml`
- Create: `ci/buildspec-infra-apply.yml`
- Create: `ci/buildspec-app.yml`

- [ ] **Step 1: Write `ci/buildspec-infra-plan.yml`**

```yaml
version: 0.2

env:
  variables:
    TF_VERSION: "1.7.5"

phases:
  install:
    runtime-versions:
      golang: latest
    commands:
      - curl -sSL https://releases.hashicorp.com/terraform/${TF_VERSION}/terraform_${TF_VERSION}_linux_amd64.zip -o tf.zip
      - unzip -q tf.zip && mv terraform /usr/local/bin/terraform
      - terraform version

  pre_build:
    commands:
      - echo "Plan target: ${TF_STACK_DIR}"
      - cd ${TF_STACK_DIR}
      - terraform init -input=false
      - terraform fmt -check -recursive
      - terraform validate

  build:
    commands:
      - cd ${TF_STACK_DIR}
      - terraform plan -input=false -out=tfplan -detailed-exitcode || PLAN_EXIT=$?
      - echo "Plan exit code (0=no-changes, 2=changes): ${PLAN_EXIT:-0}"
      - terraform show -no-color tfplan > tfplan.txt
      - echo "--- plan summary ---" && head -100 tfplan.txt

artifacts:
  base-directory: .
  files:
    - "**/*"
```

- [ ] **Step 2: Write `ci/buildspec-infra-apply.yml`**

```yaml
version: 0.2

env:
  variables:
    TF_VERSION: "1.7.5"

phases:
  install:
    runtime-versions:
      golang: latest
    commands:
      - curl -sSL https://releases.hashicorp.com/terraform/${TF_VERSION}/terraform_${TF_VERSION}_linux_amd64.zip -o tf.zip
      - unzip -q tf.zip && mv terraform /usr/local/bin/terraform
      - terraform version

  pre_build:
    commands:
      - cd ${TF_STACK_DIR}
      - terraform init -input=false

  build:
    commands:
      - cd ${TF_STACK_DIR}
      - test -f tfplan || (echo "ERROR: tfplan artifact missing — did the Plan stage succeed?" && exit 1)
      - terraform apply -input=false -auto-approve tfplan
```

- [ ] **Step 3: Write `ci/buildspec-app.yml`**

```yaml
version: 0.2

# Dual-phase buildspec: same file used by both app-build and app-deploy
# CodeBuild projects. The APP_PHASE env var (set in the CodeBuild project
# config) selects which logic runs.

phases:
  install:
    runtime-versions:
      golang: 1.24
    commands:
      - go version

  pre_build:
    commands:
      - echo "Phase = ${APP_PHASE}"
      - SHORT_SHA=$(echo "${CODEBUILD_RESOLVED_SOURCE_VERSION}" | cut -c1-7)
      - IMAGE_URI="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/${ECR_REPO}:${SHORT_SHA}"
      - echo "IMAGE_URI=${IMAGE_URI}"
      - aws ecr get-login-password --region "${AWS_REGION}" | docker login --username AWS --password-stdin "${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"

  build:
    commands:
      # BUILD PHASE — only when APP_PHASE=build
      - |
        if [ "${APP_PHASE}" = "build" ]; then
          echo "=== Running tests ==="
          go test ./... -count=1 || (echo "tests failed" && exit 1)
          echo "=== Building Docker image ==="
          docker build -t "${IMAGE_URI}" .
          docker push "${IMAGE_URI}"
          echo "${IMAGE_URI}" > image_uri.txt
        fi

      # DEPLOY PHASE — only when APP_PHASE=deploy
      - |
        if [ "${APP_PHASE}" = "deploy" ]; then
          echo "=== Deploying ${IMAGE_URI} ==="
          # Discover the API ECS service (created by Plan 8). If it doesn't
          # exist yet, treat this as a no-op success — the image is in ECR
          # and ready for Plan 8 to pick up.
          SERVICE_NAME="${APP_NAME_PREFIX}-api"
          CLUSTER_NAME="${APP_NAME_PREFIX}-cluster"

          if aws ecs describe-services --cluster "${CLUSTER_NAME}" --services "${SERVICE_NAME}" --region "${AWS_REGION}" 2>/dev/null | grep -q '"status": "ACTIVE"'; then
            # Fetch current task definition, swap the image, register a new revision.
            TASK_DEF_ARN=$(aws ecs describe-services --cluster "${CLUSTER_NAME}" --services "${SERVICE_NAME}" --region "${AWS_REGION}" --query 'services[0].taskDefinition' --output text)
            aws ecs describe-task-definition --task-definition "${TASK_DEF_ARN}" --region "${AWS_REGION}" --query 'taskDefinition' > taskdef.json
            jq --arg img "${IMAGE_URI}" '
              .containerDefinitions[0].image = $img |
              del(.taskDefinitionArn, .revision, .status, .requiresAttributes, .compatibilities, .registeredAt, .registeredBy)
            ' taskdef.json > taskdef.new.json
            NEW_TASK_DEF=$(aws ecs register-task-definition --cli-input-json file://taskdef.new.json --region "${AWS_REGION}" --query 'taskDefinition.taskDefinitionArn' --output text)
            aws ecs update-service --cluster "${CLUSTER_NAME}" --service "${SERVICE_NAME}" --task-definition "${NEW_TASK_DEF}" --region "${AWS_REGION}" > /dev/null
            echo "Deployed ${NEW_TASK_DEF} to ${SERVICE_NAME}"
          else
            echo "Service ${SERVICE_NAME} not found in cluster ${CLUSTER_NAME} (Plan 8 not yet applied). Image is in ECR; skipping deploy."
          fi
        fi

artifacts:
  files:
    - image_uri.txt
  discard-paths: yes
```

- [ ] **Step 4: Verify YAML is well-formed**

```bash
python3 -c "import yaml; yaml.safe_load(open('ci/buildspec-infra-plan.yml'))"
python3 -c "import yaml; yaml.safe_load(open('ci/buildspec-infra-apply.yml'))"
python3 -c "import yaml; yaml.safe_load(open('ci/buildspec-app.yml'))"
```

Expected: no output (success).

- [ ] **Step 5: Commit**

```bash
git add ci/buildspec-infra-plan.yml ci/buildspec-infra-apply.yml ci/buildspec-app.yml
git commit -m "feat(ci): buildspecs for infra plan/apply + app build/deploy"
```

---

### Task 12: README — bootstrap quickstart

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Append Plan 7 quickstart at the end of README**

````markdown

## Plan 7 quickstart — Terraform bootstrap + CI/CD pipelines

Bootstrap creates the AWS-side scaffolding: state backend (S3 + DynamoDB),
GitHub CodeStar connection, ECR repo, SNS approval topic, IAM roles, and
two CodePipelines (infra + app). Run this **once** from a developer laptop
with AWS admin credentials.

```bash
cd terraform/bootstrap

# Configure your inputs
cp terraform.tfvars.example terraform.tfvars
# Open terraform.tfvars and set approval_email at minimum.

# Apply
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

After apply, the outputs print three required follow-ups:

1. **Authorize the GitHub CodeStar Connection.** Terraform creates it in
   `PENDING` state. Go to AWS Console → Developer Tools → Settings →
   Connections → `hwh-github` → Update pending connection → authorize the
   `AWS Connector for GitHub` app on the `wmyers/heres-whats-happening` repo.
   Until you do this, the pipelines fail at their Source stage.

2. **Confirm the SNS email subscription.** AWS emails the address you set
   in `approval_email`. Click the link to confirm. Without confirmation,
   manual-approval notifications won't arrive.

3. **Store the local Terraform state file safely.**
   `terraform/bootstrap/terraform.tfstate` is gitignored. Copy it to a
   password manager / encrypted volume — without it you can't update
   bootstrap resources later.

The pipelines exist and will run on every push to `master`, but they're
no-op until:
- `terraform/prod/` is populated (Plan 8) — the infra pipeline will then
  start producing meaningful plans for the manual-approval gate.
- An ECS service exists (Plan 8) — the app pipeline's Deploy stage will
  then actually update the running task definition. Until then, it just
  pushes the image to ECR.
````

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: Plan 7 Terraform bootstrap quickstart"
```

---

### Task 13: Final validation pass

**Files:**
- (No code changes; validation + cleanup only)

This task runs the final offline-only checks across all of Plan 7's output before declaring the branch ready to merge.

- [ ] **Step 1: Terraform formatting + validation across the stack**

```bash
cd terraform/bootstrap
terraform fmt -check -recursive
terraform validate
```

Expected: no formatting issues; `Success! The configuration is valid.`

If `terraform fmt -check` flags anything, run `terraform fmt -recursive` and amend the relevant commit with the formatting fix.

- [ ] **Step 2: YAML lint on buildspecs**

```bash
for f in ci/buildspec-*.yml; do
  python3 -c "import yaml,sys; yaml.safe_load(open(sys.argv[1])); print(sys.argv[1], 'ok')" "$f"
done
```

Expected: three lines ending in ` ok`.

- [ ] **Step 3: Go test suite remains green (Plan 7 should not have broken Plans 1–5)**

```bash
make test
```

Expected: all packages pass.

- [ ] **Step 4: Confirm bootstrap state files are gitignored**

```bash
cd terraform/bootstrap
touch terraform.tfstate .terraform/probe
git check-ignore terraform.tfstate
git check-ignore .terraform
rm terraform.tfstate; rm -rf .terraform
```

Expected: both `check-ignore` commands print the matched paths (proving they're ignored). If either fails, fix the `.gitignore`.

- [ ] **Step 5: If anything failed above, fix it inline + commit (no separate task needed). Otherwise, no commit.**

---

## Self-Review

**Spec coverage check (Plan 7 scope only):**

| Spec requirement (from design.md §12) | Implemented in |
|---|---|
| Local state for bootstrap stack | Task 2 (no `backend` block → defaults to local) + Task 1 (.gitignore) |
| S3 state bucket for prod stack (versioning + SSE) | Task 3 |
| DynamoDB lock table | Task 3 |
| GitHub CodeStar Connection | Task 4 |
| ECR repo for the Go app | Task 5 |
| SNS topic for manual approval | Task 6 |
| CodePipelines (infra + app) | Task 9 |
| CodeBuild projects (4) | Task 8 |
| IAM roles (per-pipeline + per-CodeBuild) | Task 7 |
| Plan → Manual Approval → Apply gating | Task 9 (infra pipeline stages) |
| App build → ECR push → Deploy | Task 8 + Task 11 buildspec-app.yml |
| Single-image multi-subcommand strategy | Task 5 (one ECR repo); buildspec-app.yml builds one image |
| Pipeline triggers on master push | Task 9 (`DetectChanges = "true"` on Source action) |
| Plan/apply uses pinned Terraform version | Task 1 (`.terraform-version`) + buildspecs (`TF_VERSION`) |
| `terraform fmt -check` + `validate` enforced in plan stage | Task 11 buildspec-infra-plan.yml |
| Pipelines artifact bucket | Task 7 |

**Deferred to Plan 8 (per spec):**

- `terraform/prod/` actual resources (VPC, ECS, RDS, SQS, ALB, Secrets Manager, etc.)
- ECS services + EventBridge schedules — the app-deploy buildspec gracefully no-ops when these don't exist yet.
- The actual `app-deploy` Service-Discovery / TaskDef-revision updates start working once Plan 8 creates the cluster + service.

**Deliberate v1 simplifications:**

- CodeBuild infra role uses AWS `AdministratorAccess` managed policy rather than enumerated minimum-privilege. Terraform creates many resource types; explicit policy authoring is its own multi-day project. The TODO is to tighten this once the `prod` stack stabilizes.
- App pipeline runs on every push to `master`, including pushes that only touch `terraform/` or `docs/`. CodePipeline source-path filtering exists but is awkward in CodeStar Source actions; for v1 the build is fast enough (~3 min) that running on every push is acceptable.
- The buildspec hard-codes service/cluster name `hwh-api` / `hwh-cluster`. Plan 8 must name its ECS resources consistently or Plan 8 introduces variable indirection.

**Placeholder scan:** no "TBD", "implement later", or "add error handling" steps. Every code-touching step has complete code.

**Type consistency:**

- Resource name prefix `hwh-` is consistent across all tasks.
- `app_name_prefix` variable is referenced in Tasks 3, 5, 6, 7, 8, 9 — all use `${var.app_name_prefix}-...` form.
- `aws_codestarconnections_connection.github.arn` is referenced from `iam.tf` (Task 7) and `pipelines.tf` (Task 9). Same resource name.
- `aws_sns_topic.infra_approval.arn` referenced in Task 7 and Task 9. Same name.
- `aws_s3_bucket.pipeline_artifacts.arn` defined in Task 7 (alongside the IAM roles); referenced in Task 9.
- `aws_ecr_repository.app.{arn,name,repository_url}` defined in Task 5; referenced in Tasks 7 (IAM) and 8 (CodeBuild env vars).

**Plan-internal consistency:**

- The "single image, multi-subcommand" pattern from Plan 1's spec is honored: one ECR repo, one Docker build. The app-deploy phase will eventually update multiple ECS task definitions (api service + each scheduled task) — that's Plan 8 work, not Plan 7.
- Buildspecs reference `ci/buildspec-*.yml` paths from the *repo root*; the CodeBuild source type is `CODEPIPELINE`, so it sees the repo root. Paths are correct.
- The infra pipeline's Apply stage takes `plan_output` as input, which is the artifact uploaded by the Plan stage (containing the saved `tfplan` file). The buildspec-infra-apply.yml correctly reads `tfplan` from the CWD.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-20-plan-07-terraform-bootstrap.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
