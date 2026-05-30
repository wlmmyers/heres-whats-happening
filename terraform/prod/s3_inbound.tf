resource "aws_s3_bucket" "inbound_email" {
  bucket = "${var.app_name_prefix}-inbound-email"
  tags   = { App = var.app_name_prefix }
}

resource "aws_s3_bucket_public_access_block" "inbound_email" {
  bucket                  = aws_s3_bucket.inbound_email.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Raw emails are an audit trail, not permanent storage.
resource "aws_s3_bucket_lifecycle_configuration" "inbound_email" {
  bucket = aws_s3_bucket.inbound_email.id
  rule {
    id     = "expire-raw"
    status = "Enabled"
    filter {
      prefix = "raw/"
    }
    expiration {
      days = 90
    }
  }
}

# Allow SES (this account only) to write inbound mail into the bucket.
# Service-principal policy, not a public policy — compatible with the public-access block.
data "aws_iam_policy_document" "inbound_email" {
  statement {
    sid     = "AllowSESPuts"
    effect  = "Allow"
    actions = ["s3:PutObject"]
    principals {
      type        = "Service"
      identifiers = ["ses.amazonaws.com"]
    }
    resources = ["${aws_s3_bucket.inbound_email.arn}/*"]
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.current.account_id]
    }
  }
}

resource "aws_s3_bucket_policy" "inbound_email" {
  bucket = aws_s3_bucket.inbound_email.id
  policy = data.aws_iam_policy_document.inbound_email.json
}

# S3 -> Lambda on new object under raw/.
resource "aws_s3_bucket_notification" "inbound_email" {
  bucket = aws_s3_bucket.inbound_email.id
  lambda_function {
    lambda_function_arn = aws_lambda_function.email_parser.arn
    events              = ["s3:ObjectCreated:*"]
    filter_prefix       = "raw/"
  }
  depends_on = [aws_lambda_permission.allow_s3_invoke]
}
