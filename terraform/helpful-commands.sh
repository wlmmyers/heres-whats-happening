# Initialise the prod stack locally. The state bucket name embeds the account ID,
# which is not committed to the repo, so derive it from STS at init time.
# Run from terraform/prod.
terraform init -input=false \
    -backend-config="bucket=hwh-tf-state-$(AWS_PROFILE=servant aws sts get-caller-identity --query Account --output text)"

# Seed the "foo" secret in AWS Secrets Manager
AWS_PROFILE=profile aws secretsmanager put-secret-value \
    --secret-id hwh/foo \
    --secret-string "bar"

# Seed an env var in SSM Parameter Store
AWS_PROFILE=profile aws ssm put-parameter \
    --name /hwh/foo \
    --value "bar" \
    --type String \
    --overwrite
