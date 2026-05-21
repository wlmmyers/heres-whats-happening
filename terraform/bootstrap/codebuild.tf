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
