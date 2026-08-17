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

# Check the SESv2 account settings for the prod account
AWS_PROFILE=servant aws sesv2 get-account --region us-east-1 \
  --query '{Prod:ProductionAccessEnabled,Max24:SendQuota.Max24HourSend,Rate:SendQuota.MaxSendRate}'

# Run SQL query against prod, write output to file
PGPASSWORD=$$(aws secretsmanager get-secret-value --profile $(AWS_PROFILE) --region $(PROD_REGION) \
    --secret-id $(PROD_SECRET_ARN) --query SecretString --output text | jq -r '.password') \
    psql "host=127.0.0.1 port=5432 dbname=appdb user=app sslmode=require"  \
    --no-align --tuples-only --file /path/to/query.sql > /path/to/output.jsonl

