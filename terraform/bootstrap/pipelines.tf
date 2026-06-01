# ---------------------------------------------------------------------------
# Infra pipeline: Source → Plan → Manual Approval → Apply
# ---------------------------------------------------------------------------

resource "aws_codepipeline" "infra" {
  name          = "${var.app_name_prefix}-infra-pipeline"
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
            "terraform/**",
            "ci/buildspec-infra-plan.yml",
            "ci/buildspec-infra-apply.yml",
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
  name          = "${var.app_name_prefix}-app-pipeline"
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
            "cmd/**",
            "internal/**",
            "go.mod",
            "go.sum",
            "Dockerfile",
            "docker-compose.yml",
            "web/**",
            "sql/**",
            "sqlc.yaml",
            "ci/buildspec-app.yml",
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
      name     = "DeployECS"
      category = "Build"
      owner    = "AWS"
      provider = "CodeBuild"
      version  = "1"
      # Read from source_output, not build_output: CodeBuild locates its
      # buildspec (ci/buildspec-app.yml) relative to the input artifact, and
      # only the source artifact carries the ci/ dir. Deploy recomputes
      # IMAGE_URI from the source revision, so it needs nothing from the build.
      input_artifacts = ["source_output"]

      configuration = {
        ProjectName = aws_codebuild_project.app_deploy.name
      }
    }
  }
}

# ---------------------------------------------------------------------------
# Lambda pipeline: Source → BuildAndDeploy
# (the lambda buildspec builds+pushes the image AND runs update-function-code,
# so a single Build stage covers both; no separate deploy stage or approval.)
# ---------------------------------------------------------------------------

resource "aws_codepipeline" "lambda" {
  name          = "${var.app_name_prefix}-lambda-pipeline"
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
            "lambda/**",
            "ci/buildspec-lambda.yml",
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
      name            = "BuildPushDeploy"
      category        = "Build"
      owner           = "AWS"
      provider        = "CodeBuild"
      version         = "1"
      input_artifacts = ["source_output"]

      configuration = {
        ProjectName = aws_codebuild_project.lambda_build.name
      }
    }
  }
}
