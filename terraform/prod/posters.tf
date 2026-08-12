# Generated concert posters (svg + png). Private; served via presigned URLs.
resource "aws_s3_bucket" "posters" {
  bucket = "${var.app_name_prefix}-posters-${data.aws_caller_identity.current.account_id}"
  tags   = { App = var.app_name_prefix }
}

resource "aws_s3_bucket_public_access_block" "posters" {
  bucket                  = aws_s3_bucket.posters.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "posters" {
  bucket = aws_s3_bucket.posters.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Function URL for the poster path. AWS_IAM so only an authorized principal
# (the ECS task role, granted in iam.tf) can call it.
# RESPONSE_STREAM lets the workflow run past CloudFront's buffered-origin read timeout.
resource "aws_lambda_function_url" "mastra_handler" {
  function_name      = aws_lambda_function.mastra_handler.function_name
  authorization_type = "AWS_IAM"
  invoke_mode        = "RESPONSE_STREAM"
}
