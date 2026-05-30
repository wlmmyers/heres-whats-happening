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
