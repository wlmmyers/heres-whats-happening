#!/usr/bin/env bash
#
# taskdef-edit.sh — set/unset env vars and secret refs on a live ECS task def
# and register a new revision. See docs/superpowers/specs/2026-05-29-taskdef-edit-helper-design.md
#
# Env-overridable defaults (match the cutover script + Makefile):
#   AWS_PROFILE=servant AWS_DEFAULT_REGION=us-east-1 CLUSTER=hwh-cluster FAMILY=hwh-api
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: taskdef-edit.sh [--family hwh-api] (--set-env KEY=VALUE | --set-secret NAME=REF | --unset NAME)... [--deploy] [--yes] [--dry-run]
  --set-env KEY=VALUE    upsert a plain env var
  --set-secret NAME=REF  upsert a secret ref (arn:... verbatim, else a Secrets Manager name)
  --unset NAME           remove NAME from env and secrets
  --deploy               roll hwh-api via update-service after registering (api only)
  --yes                  skip the confirmation prompt
  --dry-run              render the new task def to stdout; do not register
EOF
}

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

SET_ENV=() ; SET_SECRET=() ; UNSET=() ; DEPLOY=0 ; YES=0 ; DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --family)     [[ $# -ge 2 ]] || { echo "error: $1 requires a value" >&2; exit 2; }; FAMILY="$2"; shift 2 ;;
    --set-env)    [[ $# -ge 2 ]] || { echo "error: $1 requires a value" >&2; exit 2; }; SET_ENV+=("$2"); shift 2 ;;
    --set-secret) [[ $# -ge 2 ]] || { echo "error: $1 requires a value" >&2; exit 2; }; SET_SECRET+=("$2"); shift 2 ;;
    --unset)      [[ $# -ge 2 ]] || { echo "error: $1 requires a value" >&2; exit 2; }; UNSET+=("$2"); shift 2 ;;
    --deploy)     DEPLOY=1; shift ;;
    --yes|-y)     YES=1; shift ;;
    --dry-run)    DRY_RUN=1; shift ;;
    -h|--help)    usage; exit 0 ;;
    *) echo "error: unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if ((${#SET_ENV[@]} + ${#SET_SECRET[@]} + ${#UNSET[@]} == 0)); then
  echo "error: at least one of --set-env / --set-secret / --unset is required" >&2
  usage; exit 2
fi
if ((DEPLOY)) && [[ "$FAMILY" != "hwh-api" ]]; then
  echo "error: --deploy only applies to hwh-api (the only ECS service); '$FAMILY' is scheduled and picks up :LATEST on next firing" >&2
  exit 2
fi

# Build a JSON array of {name,value} from the --set-env pairs.
ENV_JSON='[]'
if ((${#SET_ENV[@]})); then
  for kv in "${SET_ENV[@]}"; do
    [[ "$kv" == *=* ]] || { echo "error: --set-env expects KEY=VALUE, got '$kv'" >&2; exit 2; }
    name=${kv%%=*}; value=${kv#*=}
    ENV_JSON=$(jq -c --arg n "$name" --arg v "$value" '. + [{name:$n, value:$v}]' <<<"$ENV_JSON")
  done
fi

# Build a JSON array of {name,valueFrom} from the --set-secret pairs.
SEC_JSON='[]'
if ((${#SET_SECRET[@]})); then
  for nv in "${SET_SECRET[@]}"; do
    [[ "$nv" == *=* ]] || { echo "error: --set-secret expects NAME=REF, got '$nv'" >&2; exit 2; }
    name=${nv%%=*}; ref=${nv#*=}
    valueFrom=$(resolve_secret_ref "$ref")
    SEC_JSON=$(jq -c --arg n "$name" --arg vf "$valueFrom" '. + [{name:$n, valueFrom:$vf}]' <<<"$SEC_JSON")
  done
fi

# Build a JSON array of names to remove from both environment and secrets.
UNSET_JSON='[]'
if ((${#UNSET[@]})); then
  UNSET_JSON=$(printf '%s\n' "${UNSET[@]}" | jq -R . | jq -cs .)
fi

WORKDIR=$(mktemp -d); trap 'rm -rf "$WORKDIR"' EXIT

# Current revision. AWS fetch is added in a later task; for now require a file.
: "${TASKDEF_INPUT:?TASKDEF_INPUT must point to a task-def JSON (AWS fetch added later)}"
cp "$TASKDEF_INPUT" "$WORKDIR/current.json"

# Upsert env/secrets and drop --unset names from both; strip metadata.
jq --argjson envUp "$ENV_JSON" --argjson secUp "$SEC_JSON" --argjson unset "$UNSET_JSON" '
  ($envUp | map(.name)) as $en
  | ($secUp | map(.name)) as $sn
  | ($en + $unset) as $edrop
  | ($sn + $unset) as $sdrop
  | .containerDefinitions[0].environment =
      (((.containerDefinitions[0].environment // [])
        | map(select(.name as $x | ($edrop | index($x)) == null))) + $envUp)
  | .containerDefinitions[0].secrets =
      (((.containerDefinitions[0].secrets // [])
        | map(select(.name as $x | ($sdrop | index($x)) == null))) + $secUp)
  | del(.taskDefinitionArn, .revision, .status, .requiresAttributes,
        .compatibilities, .registeredAt, .registeredBy)
' "$WORKDIR/current.json" > "$WORKDIR/new.json"

# No-op guard: compare env+secrets as order-insensitive name->value maps.
# The upsert reorders entries, so a naive byte-compare would misread that as a
# change; -S sorts keys for a stable comparison.
maps() {
  jq -S '{
    env: ((.containerDefinitions[0].environment // []) | map({(.name): .value})    | add // {}),
    sec: ((.containerDefinitions[0].secrets     // []) | map({(.name): .valueFrom}) | add // {})
  }' "$1"
}
current_maps=$(maps "$WORKDIR/current.json")
new_maps=$(maps "$WORKDIR/new.json")
if [[ "$current_maps" == "$new_maps" ]]; then
  echo "no changes — nothing to register"
  exit 0
fi

if ((DRY_RUN)); then cat "$WORKDIR/new.json"; exit 0; fi
