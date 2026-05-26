terraform {
  backend "s3" {
    # Fill in <ACCOUNT_ID> after Plan 7's bootstrap apply prints `tf_state_bucket`.
    # Or pass via: terraform init -backend-config="bucket=hwh-tf-state-<ACCOUNT_ID>"
    bucket         = "hwh-tf-state-REPLACE_WITH_ACCOUNT_ID"
    key            = "prod/terraform.tfstate"
    region         = "us-east-1"
    dynamodb_table = "hwh-tf-state-lock"
    encrypt        = true
  }
}
