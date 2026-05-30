resource "aws_ecr_repository" "email_parser" {
  name                 = "${var.app_name_prefix}-email-parser"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = { App = var.app_name_prefix }
}

# Keep only recent images.
resource "aws_ecr_lifecycle_policy" "email_parser" {
  repository = aws_ecr_repository.email_parser.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Expire untagged images after 14 days"
      selection    = { tagStatus = "untagged", countType = "sinceImagePushed", countUnit = "days", countNumber = 14 }
      action       = { type = "expire" }
    }]
  })
}
