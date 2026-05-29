# Auto-loaded by Terraform (*.auto.tfvars). Committed on purpose: the apex domain is
# not a secret, and the CodePipeline source checkout needs it so `domain_name` does not
# fall back to its "example.com" default during plan/apply.
domain_name = "hereswhatshappening.app"

# Optional overrides:
aws_region               = "us-east-1"
app_name_prefix          = "hwh"
ticketmaster_city        = "Seattle"
db_instance_class        = "db.t4g.small"
db_backup_retention_days = 7
ingest_workers           = 4
