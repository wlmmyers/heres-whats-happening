terraform {
  backend "s3" {
    # `bucket` is intentionally omitted (partial backend config) so the AWS account
    # ID — which is part of the bucket name — is not committed to the repo. It is
    # supplied at init time:
    #   - CI: CodeBuild injects TF_STATE_BUCKET and the buildspec passes
    #     `-backend-config="bucket=$TF_STATE_BUCKET"`.
    #   - Local: see terraform/helpful-commands.sh for the init command.
    key            = "prod/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "hwh-tf-state-lock"
    encrypt        = true
  }
}
