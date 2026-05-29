#!/usr/bin/env bash
#
# cutover-register-taskdef.sh — Task 8, Step 2 of the DSN-from-components cutover.
# See docs/superpowers/plans/2026-05-29-dsn-from-components.md
#
# The api task def has `ignore_changes = [container_definitions]`, so `terraform
# apply` does NOT push the DB_* env/secrets to the live task def. This script
# performs the one-time live flip: it takes the CURRENT hwh-api task def, swaps
# the old single DATABASE_URL secret for DB_* env vars + DB_USER/DB_PASSWORD
# JSON-key secret refs (off the RDS-managed master secret), and registers a new
# revision. It is idempotent — safe to re-run.
#
# It does NOT migrate, roll the service, or run terraform — those are deliberate,
# gated follow-up steps printed at the end.
#
# Prerequisites:
#   - Step 1 done: master pushed, app pipeline built + registered the NEW image
#     onto a hwh-api revision (this script GATES on that and stops otherwise).
#   - Tools: aws CLI, jq, terraform. AWS creds for the prod account.
#
# Overridable via env (defaults match the Makefile + terraform/prod):
#   AWS_PROFILE=servant  AWS_DEFAULT_REGION=us-east-1  CLUSTER=hwh-cluster
#   FAMILY=hwh-api  DB_NAME=appdb  DB_SSLMODE=require
set -euo pipefail

: "${AWS_PROFILE:=servant}"
: "${AWS_DEFAULT_REGION:=us-east-1}"
: "${CLUSTER:=hwh-cluster}"
: "${FAMILY:=hwh-api}"
: "${DB_NAME:=appdb}"
: "${DB_SSLMODE:=require}"
export AWS_PROFILE AWS_DEFAULT_REGION

for bin in aws jq terraform; do
  command -v "$bin" >/dev/null 2>&1 || { echo "error: '$bin' not found on PATH" >&2; exit 1; }
done

REPO_ROOT=$(git rev-parse --show-toplevel)

# ── resolve the AWS-assigned values from terraform state (read-only) ──────────
MASTER_ARN=$(terraform -chdir="$REPO_ROOT/terraform/prod" output -raw db_master_user_secret_arn)
ENDPOINT=$(terraform -chdir="$REPO_ROOT/terraform/prod" output -raw db_endpoint)   # host:port
DB_HOST=${ENDPOINT%:*}
DB_PORT=${ENDPOINT##*:}
echo "profile=$AWS_PROFILE region=$AWS_DEFAULT_REGION family=$FAMILY"
echo "MASTER_ARN=$MASTER_ARN"
echo "DB_HOST=$DB_HOST  DB_PORT=$DB_PORT  DB_NAME=$DB_NAME  DB_SSLMODE=$DB_SSLMODE"

# ── GATE: the latest revision must already carry the NEW image ────────────────
# If this is :bootstrap or a stale SHA, the pipeline (Step 1) has not deployed the
# new code yet. Migrating now would run OLD code that reads DATABASE_URL, not DB_*.
IMAGE=$(aws ecs describe-task-definition --task-definition "$FAMILY" \
  --query 'taskDefinition.containerDefinitions[0].image' --output text)
echo "current $FAMILY image: $IMAGE"
case "$IMAGE" in
  *:bootstrap) echo "error: image is still :bootstrap — run Step 1 (push master, let the app pipeline build+deploy) first." >&2; exit 1 ;;
esac

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

# ── dump the live task def and flip env/secrets to DB_* (idempotent) ──────────
aws ecs describe-task-definition --task-definition "$FAMILY" \
  --query 'taskDefinition' > "$WORKDIR/taskdef.json"

jq --arg masterArn "$MASTER_ARN" --arg host "$DB_HOST" --arg port "$DB_PORT" \
   --arg dbname "$DB_NAME" --arg sslmode "$DB_SSLMODE" '
  .containerDefinitions[0].environment = (
    (.containerDefinitions[0].environment // []
      | map(select(.name | startswith("DB_") | not)))
    + [ {name:"DB_HOST",    value:$host},
        {name:"DB_PORT",    value:$port},
        {name:"DB_NAME",    value:$dbname},
        {name:"DB_SSLMODE", value:$sslmode} ]
  )
  | .containerDefinitions[0].secrets = (
    (.containerDefinitions[0].secrets // []
      | map(select(.name != "DATABASE_URL" and .name != "DB_USER" and .name != "DB_PASSWORD")))
    + [ {name:"DB_USER",     valueFrom:($masterArn + ":username::")},
        {name:"DB_PASSWORD", valueFrom:($masterArn + ":password::")} ]
  )
  | del(.taskDefinitionArn, .revision, .status, .requiresAttributes,
        .compatibilities, .registeredAt, .registeredBy)
' "$WORKDIR/taskdef.json" > "$WORKDIR/taskdef.new.json"

# ── review before registering ─────────────────────────────────────────────────
echo "=== new environment ==="; jq '.containerDefinitions[0].environment' "$WORKDIR/taskdef.new.json"
echo "=== new secrets ===";     jq '.containerDefinitions[0].secrets'     "$WORKDIR/taskdef.new.json"

# ── register the new revision ─────────────────────────────────────────────────
# If register rejects an unexpected field (e.g. enableFaultInjection on newer
# AWS CLI), add that field to the del(...) list above — same del-list as
# ci/buildspec-app.yml.
NEW_ARN=$(aws ecs register-task-definition \
  --cli-input-json "file://$WORKDIR/taskdef.new.json" \
  --query 'taskDefinition.taskDefinitionArn' --output text)
echo "registered: $NEW_ARN"

cat <<EOF

Next (deliberate, gated) steps:
  3. make migrate-prod && make migrate-prod-status   # expect exitCode 0, "migrations applied"
  4. aws ecs update-service --cluster $CLUSTER --service $FAMILY \\
       --task-definition $NEW_ARN --region $AWS_DEFAULT_REGION
  5. (only after step 4) cd terraform/prod && terraform apply   # drops the dead database-url secret
EOF
