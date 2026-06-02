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
    # Docker Hub creds for authenticated pulls — pulled from the JSON secret's keys.
    environment_variable {
      name  = "DOCKERHUB_USER"
      value = "${aws_secretsmanager_secret.dockerhub.name}:username"
      type  = "SECRETS_MANAGER"
    }
    environment_variable {
      name  = "DOCKERHUB_TOKEN"
      value = "${aws_secretsmanager_secret.dockerhub.name}:token"
      type  = "SECRETS_MANAGER"
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

# ---------------------------------------------------------------------------
# Lambda (email-parser): test + Docker build + push to ECR + update function code
# (the buildspec self-deploys via `aws lambda update-function-code`, so there is
# no separate deploy stage). Base image is ECR Public + npm, so no Docker Hub auth.
# ---------------------------------------------------------------------------

resource "aws_codebuild_project" "lambda_build" {
  name          = "${var.app_name_prefix}-lambda-build"
  service_role  = aws_iam_role.codebuild_lambda.arn
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
    # Repo NAME only (not the full registry URL) — the buildspec composes the URI.
    environment_variable {
      name  = "LAMBDA_ECR_REPO"
      value = aws_ecr_repository.email_parser.name
    }
    environment_variable {
      name  = "FUNCTION_NAME"
      value = "${var.app_name_prefix}-email-parser"
    }
  }

  source {
    type      = "CODEPIPELINE"
    buildspec = "ci/buildspec-lambda.yml"
  }

  logs_config {
    cloudwatch_logs {
      group_name = "/aws/codebuild/${var.app_name_prefix}-lambda-build"
    }
  }
}

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
