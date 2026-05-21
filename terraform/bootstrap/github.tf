# CodeStar Connection to GitHub. After `terraform apply` the connection is
# created in PENDING state — finalize it via:
#   AWS Console → CodePipeline → Settings → Connections → click the connection
#   → "Update pending connection" → authorize the AWS app on GitHub.
# Until that one-time step happens, the pipelines fail at the Source stage.

resource "aws_codestarconnections_connection" "github" {
  name          = "${var.app_name_prefix}-github"
  provider_type = "GitHub"
}
