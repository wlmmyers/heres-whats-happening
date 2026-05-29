#!/usr/bin/env bash
#
# taskdef-edit.sh — set/unset env vars and secret refs on a live ECS task def
# and register a new revision. See docs/superpowers/specs/2026-05-29-taskdef-edit-helper-design.md
#
# Env-overridable defaults (match the cutover script + Makefile):
#   AWS_PROFILE=servant AWS_DEFAULT_REGION=us-east-1 CLUSTER=hwh-cluster FAMILY=hwh-api
set -euo pipefail

: "${AWS_PROFILE:=servant}"
: "${AWS_DEFAULT_REGION:=us-east-1}"
: "${CLUSTER:=hwh-cluster}"
: "${FAMILY:=hwh-api}"
export AWS_PROFILE AWS_DEFAULT_REGION

# Resolve a --set-secret REF to an ECS valueFrom string. arn: refs (incl.
# JSON-key refs like <arn>:password::) are used verbatim; a bare name is
# resolved to its secret ARN (AWS branch implemented in a later task).
resolve_secret_ref() {
  local ref=$1
  if [[ "$ref" == arn:* ]]; then
    printf '%s' "$ref"
  else
    aws secretsmanager describe-secret --secret-id "$ref" --query ARN --output text
  fi
}

SET_ENV=() ; SET_SECRET=() ; DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --family)   FAMILY="$2"; shift 2 ;;
    --set-env)     SET_ENV+=("$2"); shift 2 ;;
    --set-secret)  SET_SECRET+=("$2"); shift 2 ;;
    --dry-run)     DRY_RUN=1; shift ;;
    *) echo "error: unknown argument: $1" >&2; exit 2 ;;
  esac
done

# Build a JSON array of {name,value} from the --set-env pairs.
ENV_JSON='[]'
if ((${#SET_ENV[@]})); then
  for kv in "${SET_ENV[@]}"; do
    name=${kv%%=*}; value=${kv#*=}
    ENV_JSON=$(jq -c --arg n "$name" --arg v "$value" '. + [{name:$n, value:$v}]' <<<"$ENV_JSON")
  done
fi

# Build a JSON array of {name,valueFrom} from the --set-secret pairs.
SEC_JSON='[]'
if ((${#SET_SECRET[@]})); then
  for nv in "${SET_SECRET[@]}"; do
    name=${nv%%=*}; ref=${nv#*=}
    valueFrom=$(resolve_secret_ref "$ref")
    SEC_JSON=$(jq -c --arg n "$name" --arg vf "$valueFrom" '. + [{name:$n, valueFrom:$vf}]' <<<"$SEC_JSON")
  done
fi

WORKDIR=$(mktemp -d); trap 'rm -rf "$WORKDIR"' EXIT

# Current revision. AWS fetch is added in a later task; for now require a file.
: "${TASKDEF_INPUT:?TASKDEF_INPUT must point to a task-def JSON (AWS fetch added later)}"
cp "$TASKDEF_INPUT" "$WORKDIR/current.json"

# Upsert env vars and secret refs (replace same-name, else append); strip metadata.
jq --argjson envUp "$ENV_JSON" --argjson secUp "$SEC_JSON" '
  ($envUp | map(.name)) as $en
  | ($secUp | map(.name)) as $sn
  | .containerDefinitions[0].environment =
      (((.containerDefinitions[0].environment // [])
        | map(select(.name as $x | ($en | index($x)) == null))) + $envUp)
  | .containerDefinitions[0].secrets =
      (((.containerDefinitions[0].secrets // [])
        | map(select(.name as $x | ($sn | index($x)) == null))) + $secUp)
  | del(.taskDefinitionArn, .revision, .status, .requiresAttributes,
        .compatibilities, .registeredAt, .registeredBy)
' "$WORKDIR/current.json" > "$WORKDIR/new.json"

if ((DRY_RUN)); then cat "$WORKDIR/new.json"; exit 0; fi
