# Automated Frontend Deploy Pipeline

**Date:** 2026-06-01
**Status:** Approved

## Problem

Two related issues with the current frontend deployment setup:

1. `web/**` is included in the app pipeline's file-path trigger, causing the full Go test + Docker build + ECS deploy pipeline to execute on every frontend change — wasted CI time with no effect on the frontend.
2. Frontend deploys require a manual `web/scripts/deploy.sh` run from a developer's machine with env vars sourced from the gitignored `web/.env.deploy`.

## Solution

Add a dedicated frontend pipeline following the same pattern as the existing lambda pipeline: Source → BuildAndDeploy in a single stage. Remove `web/**` from the app pipeline trigger.

## Files Changed

| File | Change |
|---|---|
| `terraform/bootstrap/pipelines.tf` | Remove `web/**` from app trigger; add `aws_codepipeline.web` |
| `terraform/bootstrap/codebuild.tf` | Add `aws_codebuild_project.web_deploy` |
| `terraform/bootstrap/iam.tf` | Add `aws_iam_role.codebuild_web` + policy |
| `terraform/bootstrap/variables.tf` | Add `cloudfront_distribution_id` and `domain_name` |
| `terraform/bootstrap/terraform.tfvars.example` | Document the two new required vars |
| `ci/buildspec-web.yml` | New: pnpm build → s3 sync → CF invalidation |

## New Bootstrap Variables

```hcl
variable "cloudfront_distribution_id" {
  description = "CloudFront distribution ID for the frontend (from prod stack output)."
  type        = string
}

variable "domain_name" {
  description = "Apex domain (e.g. hereswhatshappening.app). Used to build VITE_API_BASE_URL."
  type        = string
}
```

Both values are already present in `terraform/prod/outputs.tfvars`. The operator copies them into `terraform/bootstrap/terraform.tfvars` after the prod stack applies.

`S3_BUCKET` is computed in terraform as `"${var.app_name_prefix}-frontend-${data.aws_caller_identity.current.account_id}"` — no new variable needed.

## `ci/buildspec-web.yml`

```yaml
version: 0.2
phases:
  install:
    commands:
      # standard image lacks Node 24 in runtime-versions (issue #803), and NVM_DIR
      # is unset without a runtime-versions nodejs entry — so install Node 24 from
      # the official tarball into /usr/local (on PATH, persists across phases).
      - NODE_TARBALL=$(curl -fsSL https://nodejs.org/dist/latest-v24.x/ | grep -o 'node-v24\.[0-9.]*-linux-x64\.tar\.xz' | head -1)
      - curl -fsSL "https://nodejs.org/dist/latest-v24.x/${NODE_TARBALL}" -o /tmp/node.tar.xz
      - tar -xJf /tmp/node.tar.xz -C /usr/local --strip-components=1
      - corepack enable && corepack prepare pnpm@latest --activate
  build:
    commands:
      - cd web
      - pnpm install --frozen-lockfile
      - pnpm run build
      - aws s3 sync dist/ "s3://${S3_BUCKET}/" --delete
      - aws cloudfront create-invalidation
          --distribution-id "${CLOUDFRONT_DISTRIBUTION_ID}"
          --paths "/*"
```

Env vars (`S3_BUCKET`, `VITE_API_BASE_URL`, `CLOUDFRONT_DISTRIBUTION_ID`) injected by the CodeBuild project definition in terraform.

## IAM Role: `codebuild_web`

Scoped to only what FE deploy needs:

- `s3:PutObject`, `s3:DeleteObject`, `s3:GetObject`, `s3:ListBucket` on the frontend bucket (`${var.app_name_prefix}-frontend-*`)
- `cloudfront:CreateInvalidation` on the specific distribution ARN
- Artifact bucket read/write (same pattern as all other codebuild roles)
- `logs:CreateLogGroup`, `logs:CreateLogStream`, `logs:PutLogEvents`

## CodeBuild Project: `web_deploy`

- Buildspec: `ci/buildspec-web.yml`
- Compute: `BUILD_GENERAL1_SMALL`
- Image: shared `aws/codebuild/standard:7.0` (no bump). Node 24 is installed from the official tarball into `/usr/local`, since the standard image doesn't expose 24 through `runtime-versions` (issue #803) and `NVM_DIR` is unset without a `runtime-versions` nodejs entry (nvm was tried first and failed for exactly that reason).
- `privileged_mode = false` (no Docker needed)
- Env vars injected:
  - `S3_BUCKET` = `"${var.app_name_prefix}-frontend-${data.aws_caller_identity.current.account_id}"`
  - `VITE_API_BASE_URL` = `"https://api.${var.domain_name}"`
  - `CLOUDFRONT_DISTRIBUTION_ID` = `var.cloudfront_distribution_id`

## CodePipeline: `web`

Two-stage pipeline, same shape as the lambda pipeline:

- **Source**: CodeStarSourceConnection → GitHub, `DetectChanges = false`
- **BuildAndDeploy**: CodeBuild project `web_deploy`

Trigger: file path `web/**` on `master` branch only.

## App Pipeline Change

Remove `web/**` from the app pipeline's `file_paths.includes` so backend-only pipelines are no longer triggered by frontend commits.

## Operator Steps

Bootstrap is always applied manually (the infra pipeline targets `terraform/prod`, not `terraform/bootstrap`). Before applying:

Add to `terraform/bootstrap/terraform.tfvars` (gitignored, local only):

```hcl
cloudfront_distribution_id = "<value from terraform/prod/outputs.tfvars>"
domain_name                = "<value from terraform/prod/prod.auto.tfvars>"
```

Then apply bootstrap manually as usual. The new pipeline becomes active immediately after apply.
