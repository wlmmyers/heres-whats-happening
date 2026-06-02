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

resource "aws_s3_bucket_server_side_encryption_configuration" "pipeline_artifacts" {
  bucket = aws_s3_bucket.pipeline_artifacts.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
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
  # Pass roles to ECS tasks and EventBridge schedules (Plan 8 will create both).
  statement {
    actions   = ["iam:PassRole"]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com", "scheduler.amazonaws.com"]
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
  # Read Docker Hub creds for authenticated CI pulls (seeded out-of-band).
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.dockerhub.arn]
  }
}

resource "aws_iam_role_policy" "codebuild_app" {
  role   = aws_iam_role.codebuild_app.id
  policy = data.aws_iam_policy_document.codebuild_app.json
}

# ---------------------------------------------------------------------------
# CodeBuild role for the email-parser Lambda build+deploy project
# ---------------------------------------------------------------------------

resource "aws_iam_role" "codebuild_lambda" {
  name               = "${var.app_name_prefix}-codebuild-lambda"
  assume_role_policy = data.aws_iam_policy_document.codebuild_assume.json
}

data "aws_iam_policy_document" "codebuild_lambda" {
  # ECR auth token (account-wide) + push to the email-parser repo only.
  statement {
    actions   = ["ecr:GetAuthorizationToken"]
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
    resources = [aws_ecr_repository.email_parser.arn]
  }
  # Deploy: point the function at the freshly pushed image (function created in the prod stack).
  statement {
    actions   = ["lambda:UpdateFunctionCode"]
    resources = ["arn:aws:lambda:${var.aws_region}:${data.aws_caller_identity.current.account_id}:function:${var.app_name_prefix}-email-parser"]
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

resource "aws_iam_role_policy" "codebuild_lambda" {
  role   = aws_iam_role.codebuild_lambda.id
  policy = data.aws_iam_policy_document.codebuild_lambda.json
}

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
