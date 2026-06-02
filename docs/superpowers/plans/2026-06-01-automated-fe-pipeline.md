# Automated Frontend Deploy Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a dedicated CodePipeline that builds and deploys the `/web` SPA to S3 + CloudFront on every `web/**` change, and stop the app pipeline from firing on frontend-only commits.

**Architecture:** Mirror the existing single-stage lambda pipeline. A new `web` CodePipeline (Source → BuildAndDeploy) runs a new `web_deploy` CodeBuild project against a new `ci/buildspec-web.yml`. A narrowly-scoped `codebuild_web` IAM role grants only frontend-bucket S3 writes and CloudFront invalidation. Two new bootstrap variables (`cloudfront_distribution_id`, `domain_name`) inject the deploy targets. The `web/**` glob is removed from the app pipeline trigger.

**Tech Stack:** Terraform (AWS provider), AWS CodePipeline + CodeBuild, pnpm/Vite SPA build.

**Context for the implementer:**
- All edited terraform lives in `terraform/bootstrap/`. This stack is applied **manually** (the infra CodePipeline only targets `terraform/prod`). Never run `terraform apply` as part of these tasks — only `terraform fmt` and `terraform validate`.
- `terraform/bootstrap/terraform.tfvars` is gitignored (see root `.gitignore`: `*.tfvars` with an exception only for `*.tfvars.example` and `terraform/prod/prod.auto.tfvars`). New required variables must therefore be added to `terraform.tfvars.example` and the operator's local `terraform.tfvars`, NOT committed with real values.
- The existing lambda pipeline (`aws_codepipeline.lambda` in `pipelines.tf`, `aws_codebuild_project.lambda_build` in `codebuild.tf`, `aws_iam_role.codebuild_lambda` in `iam.tf`) is the closest template — copy its structure.
- `data.aws_caller_identity.current` and `data.aws_region.current` already exist in `data.tf`.
- The frontend bucket is named `${var.app_name_prefix}-frontend-${account_id}` (see `terraform/prod/frontend.tf`). Bootstrap does not manage that bucket, so reference it by computed name/ARN string, not by resource reference.

---

### Task 1: Add bootstrap variables for the frontend deploy targets

**Files:**
- Modify: `terraform/bootstrap/variables.tf`
- Modify: `terraform/bootstrap/terraform.tfvars.example`

- [ ] **Step 1: Add the two variables to `variables.tf`**

Append to `terraform/bootstrap/variables.tf` (after the `approval_email` variable, which currently ends at line 34):

```hcl

variable "domain_name" {
  description = "Apex domain (e.g. hereswhatshappening.app). Used to build VITE_API_BASE_URL for the frontend build. Must match the prod stack's domain_name."
  type        = string
}

variable "cloudfront_distribution_id" {
  description = "CloudFront distribution ID serving the frontend (from the prod stack's cloudfront_distribution_id output). Used by the web pipeline's invalidation step."
  type        = string
}
```

- [ ] **Step 2: Document the new required vars in `terraform.tfvars.example`**

Replace the entire contents of `terraform/bootstrap/terraform.tfvars.example` with:

```hcl
# Copy to terraform.tfvars and fill in.
# Required:
approval_email = "you@example.com"

# Required for the frontend (web) pipeline. Both come from the prod stack:
#   domain_name                → terraform/prod/prod.auto.tfvars
#   cloudfront_distribution_id → terraform/prod/outputs.tfvars (cloudfront_distribution_id)
domain_name                = "example.com"
cloudfront_distribution_id = "EXXXXXXXXXXXXX"

# Optional overrides:
# aws_region      = "us-east-1"
# app_name_prefix = "hwh"
# github_owner    = "wmyers"
# github_repo     = "heres-whats-happening"
# github_branch   = "master"
```

- [ ] **Step 3: Add the values to the local (gitignored) `terraform.tfvars`**

The implementer must NOT commit this file. Add these two lines to `terraform/bootstrap/terraform.tfvars` (values come from the prod stack — confirmed present in `terraform/prod/outputs.tfvars` and `terraform/prod/prod.auto.tfvars`):

```hcl
domain_name                = "hereswhatshappening.app"
cloudfront_distribution_id = "E2BT8WOCCCG283"
```

- [ ] **Step 4: Validate**

Run: `terraform -chdir=terraform/bootstrap validate`
Expected: `Success! The configuration is valid.` (No "variable not set" errors, because `terraform.tfvars` now provides them.)

- [ ] **Step 5: Commit (excludes the gitignored tfvars)**

```bash
git add terraform/bootstrap/variables.tf terraform/bootstrap/terraform.tfvars.example
git commit -m "feat: add domain_name and cloudfront_distribution_id bootstrap vars"
```

---

### Task 2: Add the `codebuild_web` IAM role

**Files:**
- Modify: `terraform/bootstrap/iam.tf` (append at end, after the `codebuild_lambda` block which currently ends at line 284)

- [ ] **Step 1: Append the role, policy document, and attachment**

Add to the end of `terraform/bootstrap/iam.tf`:

```hcl

# ---------------------------------------------------------------------------
# CodeBuild role for the frontend (web) build+deploy project
# (builds the SPA, syncs to the frontend S3 bucket, invalidates CloudFront).
# The frontend bucket + distribution are owned by the prod stack, so they are
# referenced by computed name/ARN, not by resource reference.
# ---------------------------------------------------------------------------

resource "aws_iam_role" "codebuild_web" {
  name               = "${var.app_name_prefix}-codebuild-web"
  assume_role_policy = data.aws_iam_policy_document.codebuild_assume.json
}

data "aws_iam_policy_document" "codebuild_web" {
  # Sync the built SPA into the frontend bucket (with --delete, so DeleteObject too).
  statement {
    actions = ["s3:ListBucket"]
    resources = [
      "arn:aws:s3:::${var.app_name_prefix}-frontend-${data.aws_caller_identity.current.account_id}",
    ]
  }
  statement {
    actions = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
    resources = [
      "arn:aws:s3:::${var.app_name_prefix}-frontend-${data.aws_caller_identity.current.account_id}/*",
    ]
  }
  # Invalidate the CloudFront distribution after a sync.
  statement {
    actions   = ["cloudfront:CreateInvalidation"]
    resources = ["arn:aws:cloudfront::${data.aws_caller_identity.current.account_id}:distribution/${var.cloudfront_distribution_id}"]
  }
  # Artifact bucket + logs (same pattern as the other codebuild roles).
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

resource "aws_iam_role_policy" "codebuild_web" {
  role   = aws_iam_role.codebuild_web.id
  policy = data.aws_iam_policy_document.codebuild_web.json
}
```

- [ ] **Step 2: Format and validate**

Run: `terraform -chdir=terraform/bootstrap fmt && terraform -chdir=terraform/bootstrap validate`
Expected: `Success! The configuration is valid.`

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/iam.tf
git commit -m "feat: add codebuild_web IAM role for frontend deploys"
```

---

### Task 3: Add the `web_deploy` CodeBuild project

**Files:**
- Modify: `terraform/bootstrap/codebuild.tf` (append at end, after the `lambda_build` block which currently ends at line 257)

- [ ] **Step 1: Append the CodeBuild project**

Add to the end of `terraform/bootstrap/codebuild.tf`:

```hcl

# ---------------------------------------------------------------------------
# Web (frontend): build the Vite SPA, sync to the frontend S3 bucket, invalidate
# CloudFront. Single-phase (no separate deploy stage) — mirrors the lambda build.
# No Docker, so privileged_mode is false.
# ---------------------------------------------------------------------------

resource "aws_codebuild_project" "web_deploy" {
  name          = "${var.app_name_prefix}-web-deploy"
  service_role  = aws_iam_role.codebuild_web.arn
  build_timeout = 20

  artifacts {
    type = "CODEPIPELINE"
  }

  environment {
    compute_type = local.cb_compute_type
    # Shared standard:7.0 image. Its managed Node runtimes don't include 24
    # (aws/aws-codebuild-docker-images#803 — same reason buildspec-lambda.yml
    # pins 22), so the buildspec installs Node 24 via the preinstalled nvm
    # rather than via runtime-versions. No image bump needed.
    image           = local.cb_image
    type            = "LINUX_CONTAINER"
    privileged_mode = false

    environment_variable {
      name  = "AWS_REGION"
      value = var.aws_region
    }
    environment_variable {
      name  = "S3_BUCKET"
      value = "${var.app_name_prefix}-frontend-${data.aws_caller_identity.current.account_id}"
    }
    environment_variable {
      name  = "CLOUDFRONT_DISTRIBUTION_ID"
      value = var.cloudfront_distribution_id
    }
    environment_variable {
      name  = "VITE_API_BASE_URL"
      value = "https://api.${var.domain_name}"
    }
  }

  source {
    type      = "CODEPIPELINE"
    buildspec = "ci/buildspec-web.yml"
  }

  logs_config {
    cloudwatch_logs {
      group_name = "/aws/codebuild/${var.app_name_prefix}-web-deploy"
    }
  }
}
```

- [ ] **Step 2: Format and validate**

Run: `terraform -chdir=terraform/bootstrap fmt && terraform -chdir=terraform/bootstrap validate`
Expected: `Success! The configuration is valid.`

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/codebuild.tf
git commit -m "feat: add web_deploy CodeBuild project"
```

---

### Task 4: Add the `ci/buildspec-web.yml` build spec

**Files:**
- Create: `ci/buildspec-web.yml`

- [ ] **Step 1: Create the buildspec**

Create `ci/buildspec-web.yml` with exactly:

```yaml
version: 0.2

# Frontend (web) build+deploy. Builds the Vite SPA, syncs dist/ to the frontend
# S3 bucket, then invalidates CloudFront. All inputs (S3_BUCKET,
# CLOUDFRONT_DISTRIBUTION_ID, VITE_API_BASE_URL) are injected by the
# hwh-web-deploy CodeBuild project (see terraform/bootstrap/codebuild.tf).

phases:
  install:
    commands:
      # CodeBuild's standard:7.0 image doesn't expose Node 24 in runtime-versions
      # (aws/aws-codebuild-docker-images#803), and without a runtime-versions
      # nodejs entry NVM_DIR is unset — so nvm is not an option here. Install the
      # latest Node 24 (current LTS) straight from the official tarball into
      # /usr/local. /usr/local/bin is already on PATH and, being a filesystem
      # location rather than a per-shell env var, persists into the build phase
      # (CodeBuild runs each phase in its own shell). No nvm / NVM_DIR / bash
      # dependency — works under the dash phase shell too.
      - NODE_TARBALL=$(curl -fsSL https://nodejs.org/dist/latest-v24.x/ | grep -o 'node-v24\.[0-9.]*-linux-x64\.tar\.xz' | head -1)
      - echo "Installing ${NODE_TARBALL}"
      - curl -fsSL "https://nodejs.org/dist/latest-v24.x/${NODE_TARBALL}" -o /tmp/node.tar.xz
      - tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1
      - node --version    # build log shows exactly which Node ran
      - corepack enable
      - corepack prepare pnpm@latest --activate

  build:
    commands:
      # Node 24 is on PATH via /usr/local/bin (installed in the install phase),
      # so no per-shell re-sourcing is needed.
      - cd web
      - echo "Building with Node $(node --version), VITE_API_BASE_URL=${VITE_API_BASE_URL}"
      - pnpm install --frozen-lockfile
      - pnpm run build
      - echo "Syncing dist/ to s3://${S3_BUCKET}/"
      - aws s3 sync dist/ "s3://${S3_BUCKET}/" --delete
      - echo "Invalidating CloudFront ${CLOUDFRONT_DISTRIBUTION_ID}"
      - aws cloudfront create-invalidation --distribution-id "${CLOUDFRONT_DISTRIBUTION_ID}" --paths "/*"
```

Notes:
- **Node 24 (current LTS) via official tarball:** the standard image doesn't offer 24 through `runtime-versions` (per the repo's own finding in `buildspec-lambda.yml`), and `NVM_DIR` is unset without a `runtime-versions` nodejs entry — so an nvm-based approach fails with `cannot open /nvm.sh`. Installing the tarball into `/usr/local` is interpreter-agnostic (works under the dash phase shell) and persists across phases. `node --version` is echoed so the log shows what ran.
- **No re-sourcing needed in `build`:** because Node lives at `/usr/local/bin` (a filesystem path on PATH, not a per-shell env var), it is available in the separate `build`-phase shell without any setup.
- **pnpm:** `corepack` ships with Node and provides pnpm without a global `npm install -g pnpm` step.

- [ ] **Step 2: Validate YAML syntax**

Run: `python3 -c "import yaml,sys; yaml.safe_load(open('ci/buildspec-web.yml')); print('valid yaml')"`
Expected: `valid yaml`

- [ ] **Step 3: Commit**

```bash
git add ci/buildspec-web.yml
git commit -m "feat: add buildspec-web.yml for frontend build+deploy"
```

---

### Task 5: Add the `web` CodePipeline

**Files:**
- Modify: `terraform/bootstrap/pipelines.tf` (append at end, after the `lambda` pipeline which currently ends at line 266)

- [ ] **Step 1: Append the pipeline**

Add to the end of `terraform/bootstrap/pipelines.tf`:

```hcl

# ---------------------------------------------------------------------------
# Web pipeline: Source → BuildAndDeploy
# (the web buildspec builds the SPA, syncs to S3, and invalidates CloudFront in
# a single stage, so no separate deploy stage — mirrors the lambda pipeline.)
# Triggers only on web/** changes.
# ---------------------------------------------------------------------------

resource "aws_codepipeline" "web" {
  name          = "${var.app_name_prefix}-web-pipeline"
  role_arn      = aws_iam_role.codepipeline_service.arn
  pipeline_type = "V2"

  artifact_store {
    type     = "S3"
    location = aws_s3_bucket.pipeline_artifacts.bucket
  }

  trigger {
    provider_type = "CodeStarSourceConnection"
    git_configuration {
      source_action_name = "Source"
      push {
        branches {
          includes = [var.github_branch]
        }
        file_paths {
          includes = [
            "web/**",
            "ci/buildspec-web.yml",
          ]
        }
      }
    }
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
        DetectChanges    = "false"
      }
    }
  }

  stage {
    name = "BuildAndDeploy"
    action {
      name            = "BuildSyncInvalidate"
      category        = "Build"
      owner           = "AWS"
      provider        = "CodeBuild"
      version         = "1"
      input_artifacts = ["source_output"]

      configuration = {
        ProjectName = aws_codebuild_project.web_deploy.name
      }
    }
  }
}
```

- [ ] **Step 2: Format and validate**

Run: `terraform -chdir=terraform/bootstrap fmt && terraform -chdir=terraform/bootstrap validate`
Expected: `Success! The configuration is valid.`

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/pipelines.tf
git commit -m "feat: add web CodePipeline triggered on web/** changes"
```

---

### Task 6: Remove `web/**` from the app pipeline trigger

**Files:**
- Modify: `terraform/bootstrap/pipelines.tf:125-135` (the app pipeline's `file_paths.includes` list)

- [ ] **Step 1: Remove the `web/**` entry**

In `terraform/bootstrap/pipelines.tf`, find the app pipeline's trigger `file_paths.includes` block (inside `resource "aws_codepipeline" "app"`):

```hcl
        file_paths {
          includes = [
            "cmd/**",
            "internal/**",
            "go.mod",
            "go.sum",
            "Dockerfile",
            "docker-compose.yml",
            "web/**",
            "sql/**",
          ]
        }
```

Change it to (remove the `"web/**",` line only):

```hcl
        file_paths {
          includes = [
            "cmd/**",
            "internal/**",
            "go.mod",
            "go.sum",
            "Dockerfile",
            "docker-compose.yml",
            "sql/**",
          ]
        }
```

- [ ] **Step 2: Format and validate**

Run: `terraform -chdir=terraform/bootstrap fmt && terraform -chdir=terraform/bootstrap validate`
Expected: `Success! The configuration is valid.`

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/pipelines.tf
git commit -m "feat: stop app pipeline from triggering on web/** changes"
```

---

### Task 7: Add a pipeline-name output and final review

**Files:**
- Modify: `terraform/bootstrap/outputs.tf` (append after `app_pipeline_name`, currently ends at line 27)

- [ ] **Step 1: Add the output**

Add to `terraform/bootstrap/outputs.tf` after the `app_pipeline_name` output block:

```hcl

output "web_pipeline_name" {
  value = aws_codepipeline.web.name
}
```

- [ ] **Step 2: Run a full plan to confirm the intended changes**

This is a read-only dry run — `plan`, NOT `apply`. The implementer reviews the output; they do not apply.

Run: `terraform -chdir=terraform/bootstrap plan`
Expected: A plan adding (roughly) these new resources, and **no** destroys:
- `aws_iam_role.codebuild_web`
- `aws_iam_role_policy.codebuild_web`
- `aws_codebuild_project.web_deploy`
- `aws_codepipeline.web`
- (the app pipeline `aws_codepipeline.app` shows an in-place update removing `web/**` from its trigger)

If the plan shows any resource being destroyed unexpectedly, STOP and investigate before continuing.

- [ ] **Step 3: Commit**

```bash
git add terraform/bootstrap/outputs.tf
git commit -m "feat: add web_pipeline_name output"
```

---

## Post-Implementation: Operator Apply (manual, not part of automated execution)

After the branch is merged, the operator applies the bootstrap stack manually (the infra pipeline does NOT cover `terraform/bootstrap`):

1. Ensure `terraform/bootstrap/terraform.tfvars` has `domain_name` and `cloudfront_distribution_id` set (done in Task 1, Step 3 locally).
2. `terraform -chdir=terraform/bootstrap apply`
3. Verify: push a trivial `web/**` change to master → the `hwh-web-pipeline` runs and the app pipeline does NOT.

`web/scripts/deploy.sh` is retained for emergency manual deploys; no change to it.

---

## Self-Review Notes

- **Spec coverage:** All six spec file-changes map to tasks (variables → T1, IAM → T2, CodeBuild → T3, buildspec → T4, pipeline → T5, app trigger → T6). Added T7 (output + plan review) for parity with the existing `app_pipeline_name`/`infra_pipeline_name` outputs.
- **Buildspec deviation from spec:** The spec sketched `npm install -g pnpm` on Node 20; the plan uses `corepack` instead (cleaner, ships with Node) and builds on **Node 24** (current LTS) per the operator's requirement. CodeBuild's standard image doesn't expose Node 24 via `runtime-versions` (repo finding in `buildspec-lambda.yml`, issue #803), and `NVM_DIR` is unset without a `runtime-versions` nodejs entry, so Node 24 is installed from the official tarball into `/usr/local` — no CodeBuild image bump, shared `local.cb_image` (standard:7.0) unchanged for all projects.
- **Gitignore:** Task 1 explicitly separates the committed `.example` change from the local-only `terraform.tfvars` edit so real values are never committed.
- **No resource-reference to the frontend bucket/distribution:** they're owned by the prod stack, so referenced by computed ARN strings — correct for a cross-stack boundary.
