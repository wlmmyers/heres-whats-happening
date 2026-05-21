provider "aws" {
  region = var.aws_region
  default_tags {
    tags = {
      Project   = "heres-whats-happening"
      Stack     = "bootstrap"
      ManagedBy = "terraform"
    }
  }
}
