#!/usr/bin/env bash
#
# taskdef-drift-check.sh — assert the scheduled task-def families carry every
# env var and secret NAME that the reference family (hwh-api) carries.
#
# Why this exists: terraform/prod/ecs_schedules.tf sets
# `scheduled_env_vars = local.api_env_vars`, but sharing a terraform local
# propagates NOTHING to a live task definition. Both task-def resources carry
# `ignore_changes = [container_definitions]`, and ci/buildspec-app.yml
# re-registers each family from its CURRENT LIVE definition with only the image
# swapped. So a new env var reaches a family only via a manual
# `taskdef-edit.sh --set-env`, run once PER FAMILY.
#
# Forgetting the scheduled families is how commit 983e16f5 (2026-07-29) killed
# all three nightly jobs for ~3 weeks: hwh-api got EMAIL_SENDER/EMAIL_FROM_ADDRESS/
# APP_BASE_URL/API_BASE_URL, the scheduled families did not, and every nightly
# run died in config.Load() with "email confirmation requires ...".
#
# Names only, not values: config.Load() fails on empty/missing, and per-family
# values may legitimately differ. A family holding EXTRA names is not drift.
#
# Env-overridable defaults (match taskdef-edit.sh):
#   AWS_PROFILE=servant AWS_DEFAULT_REGION=us-east-1
#
# Offline/testing: set DRIFT_INPUT_DIR to a directory of <family>.json files
# (the shape of `aws ecs describe-task-definition --query taskDefinition`) to
# skip AWS entirely. See scripts/test/taskdef-drift-check.test.sh.
set -uo pipefail

usage() {
  cat >&2 <<'EOF'
usage: taskdef-drift-check.sh [--reference hwh-api] [--family F]...
  --reference FAMILY  family whose env/secret names are the required set (default: hwh-api)
  --family FAMILY     family to check; repeatable
                      (default: hwh-scrape-events-ticketmaster hwh-scrape-spotify hwh-match)
exits 0 when every checked family carries every reference name, 1 otherwise.
EOF
}

# The `servant` profile is a LOCAL convenience. CodeBuild authenticates with the
# build role's ambient credentials and has no named profile, so defaulting it
# there fails every build with "The config profile (servant) could not be
# found". Default it only outside CodeBuild, and never over an explicit value.
if [[ -z "${CODEBUILD_BUILD_ID:-}" ]]; then
  : "${AWS_PROFILE:=servant}"
  export AWS_PROFILE
fi
: "${AWS_DEFAULT_REGION:=${AWS_REGION:-us-east-1}}"
export AWS_DEFAULT_REGION

REFERENCE="hwh-api"
FAMILIES=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --reference) [[ $# -ge 2 ]] || { echo "error: $1 requires a value" >&2; exit 2; }; REFERENCE="$2"; shift 2 ;;
    --family)    [[ $# -ge 2 ]] || { echo "error: $1 requires a value" >&2; exit 2; }; FAMILIES+=("$2"); shift 2 ;;
    -h|--help)   usage; exit 0 ;;
    *) echo "error: unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if ((${#FAMILIES[@]} == 0)); then
  FAMILIES=(hwh-scrape-events-ticketmaster hwh-scrape-spotify hwh-match)
fi

command -v jq >/dev/null 2>&1 || { echo "error: 'jq' not found on PATH" >&2; exit 1; }
if [[ -z "${DRIFT_INPUT_DIR:-}" ]]; then
  command -v aws >/dev/null 2>&1 || { echo "error: 'aws' not found on PATH" >&2; exit 1; }
fi

# Emit the sorted env + secret names of a family's active task definition.
# Both lists matter: a missing secret (DB_PASSWORD, JWT_SIGNING_KEY) breaks
# config.Load() exactly like a missing plain var.
names_for() {
  local family=$1 json
  if [[ -n "${DRIFT_INPUT_DIR:-}" ]]; then
    local file="$DRIFT_INPUT_DIR/$family.json"
    [[ -f "$file" ]] || { echo "error: no fixture for family '$family' in $DRIFT_INPUT_DIR" >&2; return 1; }
    json=$(<"$file")
  else
    json=$(aws ecs describe-task-definition --task-definition "$family" --query 'taskDefinition' --output json 2>&1) \
      || { echo "error: cannot describe task definition '$family': $json" >&2; return 1; }
  fi
  jq -r '.containerDefinitions[0] | (.environment[]?.name), (.secrets[]?.name)' <<<"$json" | sort -u
}

REF_NAMES=$(names_for "$REFERENCE") || exit 1
if [[ -z "$REF_NAMES" ]]; then
  echo "error: reference family '$REFERENCE' has no env vars or secrets — refusing to pass vacuously" >&2
  exit 1
fi

echo "=== taskdef drift check (reference: $REFERENCE, $(wc -l <<<"$REF_NAMES" | tr -d ' ') names) ==="

drifted=0
for family in "${FAMILIES[@]}"; do
  fam_names=$(names_for "$family") || { drifted=1; continue; }

  missing=$(comm -23 <(printf '%s\n' "$REF_NAMES") <(printf '%s\n' "$fam_names"))
  if [[ -z "$missing" ]]; then
    printf 'ok   - %s\n' "$family"
    continue
  fi

  drifted=1
  printf 'DRIFT - %s is missing %d name(s):\n' "$family" "$(wc -l <<<"$missing" | tr -d ' ')"
  while IFS= read -r name; do
    printf '        MISSING %s\n' "$name"
  done <<<"$missing"
  printf '        repair: scripts/taskdef-edit.sh --family %s%s --yes\n' \
    "$family" "$(while IFS= read -r n; do printf ' --set-env %s=<value>' "$n"; done <<<"$missing")"
done

if ((drifted)); then
  cat >&2 <<EOF

Task-def drift detected. The listed names exist on '$REFERENCE' but not on the
family above, so that family's next run will read them as empty — and
config.Load() refuses to start without the required ones.

Copy each value from the reference family:
  aws ecs describe-task-definition --task-definition $REFERENCE \\
    --query 'taskDefinition.containerDefinitions[0].environment'
EOF
  exit 1
fi

echo "no drift: every family carries all $REFERENCE names"
