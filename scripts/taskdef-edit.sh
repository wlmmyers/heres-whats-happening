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

# jq is always needed; aws only when we'll actually call it (live fetch or any
# register/deploy). This keeps `--dry-run` + TASKDEF_INPUT (the test path)
# runnable without the aws CLI installed.
command -v jq >/dev/null 2>&1 || { echo "error: 'jq' not found on PATH" >&2; exit 1; }
if ((! DRY_RUN)) || [[ -z "${TASKDEF_INPUT:-}" ]]; then
  command -v aws >/dev/null 2>&1 || { echo "error: 'aws' not found on PATH" >&2; exit 1; }
fi

WORKDIR=$(mktemp -d); trap 'rm -rf "$WORKDIR"' EXIT

# Current revision: from TASKDEF_INPUT (tests) or live from AWS.
if [[ -n "${TASKDEF_INPUT:-}" ]]; then
  cp "$TASKDEF_INPUT" "$WORKDIR/current.json"
else
  aws ecs describe-task-definition --task-definition "$FAMILY" \
    --query 'taskDefinition' > "$WORKDIR/current.json"
fi

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

# --- diff: show what changes relative to the current revision -----------------
echo "=== changes for $FAMILY ==="
jq -rn --slurpfile c "$WORKDIR/current.json" --slurpfile n "$WORKDIR/new.json" '
  ($c[0].containerDefinitions[0].environment // [] | map({(.name): .value})     | add // {}) as $ce
  | ($n[0].containerDefinitions[0].environment // [] | map({(.name): .value})     | add // {}) as $ne
  | ($c[0].containerDefinitions[0].secrets     // [] | map({(.name): .valueFrom}) | add // {}) as $cs
  | ($n[0].containerDefinitions[0].secrets     // [] | map({(.name): .valueFrom}) | add // {}) as $ns
  | ( [ ($ne | keys[]) | select($ce[.] == null)                          | "ENV    + \(.)=\($ne[.])" ]
    + [ ($ne | keys[]) | select($ce[.] != null and $ce[.] != $ne[.])     | "ENV    ~ \(.): \($ce[.]) -> \($ne[.])" ]
    + [ ($ce | keys[]) | select($ne[.] == null)                          | "ENV    - \(.)" ]
    + [ ($ns | keys[]) | select($cs[.] == null)                          | "SECRET + \(.) -> \($ns[.])" ]
    + [ ($ns | keys[]) | select($cs[.] != null and $cs[.] != $ns[.])     | "SECRET ~ \(.): \($cs[.]) -> \($ns[.])" ]
    + [ ($cs | keys[]) | select($ns[.] == null)                          | "SECRET - \(.)" ]
    ) | .[]
'

# --- confirm before the (mutating) register -----------------------------------
if ! ((YES)); then
  reply=""
  printf 'Register new revision for %s? [y/N] ' "$FAMILY" > /dev/tty || true
  read -r reply < /dev/tty || {
    echo "error: no controlling terminal — re-run with --yes to skip confirmation" >&2
    exit 1
  }
  case "$reply" in
    y|Y) ;;
    *) echo "aborted — nothing registered"; exit 1 ;;
  esac
fi

# --- register -----------------------------------------------------------------
NEW_ARN=$(aws ecs register-task-definition \
  --cli-input-json "file://$WORKDIR/new.json" \
  --query 'taskDefinition.taskDefinitionArn' --output text)
echo "registered: $NEW_ARN"

# --- rollout ------------------------------------------------------------------
if ((DEPLOY)); then
  aws ecs update-service --cluster "$CLUSTER" --service "$FAMILY" \
    --task-definition "$NEW_ARN" > /dev/null
  echo "deployed $NEW_ARN to service $FAMILY"
elif [[ "$FAMILY" == "hwh-api" ]]; then
  cat <<EOF

Not deployed. Roll the service when ready:
  aws ecs update-service --cluster $CLUSTER --service $FAMILY --task-definition $NEW_ARN --region $AWS_DEFAULT_REGION
EOF
else
  echo "note: $FAMILY is scheduled (:LATEST) — the new revision runs on its next firing; no update-service needed."
fi
