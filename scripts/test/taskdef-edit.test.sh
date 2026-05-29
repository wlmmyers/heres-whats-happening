#!/usr/bin/env bash
# Plain-bash unit tests for scripts/taskdef-edit.sh — AWS-free.
# Drives the script via TASKDEF_INPUT (skip AWS describe) + --dry-run (render
# only) + arn: secret refs (no describe-secret). Run: make test-scripts
set -uo pipefail   # NOT -e: we capture non-zero exit codes from the script

HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT="$HERE/../taskdef-edit.sh"
FIXTURE="$HERE/fixtures/taskdef.json"
pass=0 fail=0

check() { # desc expected actual
  if [[ "$2" == "$3" ]]; then
    pass=$((pass + 1)); printf 'ok   - %s\n' "$1"
  else
    fail=$((fail + 1)); printf 'FAIL - %s\n      expected: [%s]\n      actual:   [%s]\n' "$1" "$2" "$3"
  fi
}

# --- set-env: append a new var ------------------------------------------------
out=$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-env NEW_VAR=hello)
check "set-env appends new var" \
  "hello" \
  "$(jq -r '.containerDefinitions[0].environment[]|select(.name=="NEW_VAR").value' <<<"$out")"
check "set-env preserves existing vars" \
  "info" \
  "$(jq -r '.containerDefinitions[0].environment[]|select(.name=="LOG_LEVEL").value' <<<"$out")"
check "render strips taskDefinitionArn" \
  "null" "$(jq '.taskDefinitionArn' <<<"$out")"
check "render strips revision" \
  "null" "$(jq '.revision' <<<"$out")"
check "render strips status" \
  "null" "$(jq '.status' <<<"$out")"

# --- set-env: replace an existing var (no duplicate) --------------------------
out=$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-env LOG_LEVEL=debug)
check "set-env replaces existing value" \
  "debug" \
  "$(jq -r '.containerDefinitions[0].environment[]|select(.name=="LOG_LEVEL").value' <<<"$out")"
check "set-env leaves no duplicate" \
  "1" \
  "$(jq '[.containerDefinitions[0].environment[]|select(.name=="LOG_LEVEL")]|length' <<<"$out")"

# --- set-secret: add a ref with an arn: value (verbatim) ----------------------
STRIPE_ARN='arn:aws:secretsmanager:us-east-1:111111111111:secret:hwh/stripe-key-AbCdEf'
out=$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-secret STRIPE_KEY="$STRIPE_ARN")
check "set-secret adds ref verbatim" \
  "$STRIPE_ARN" \
  "$(jq -r '.containerDefinitions[0].secrets[]|select(.name=="STRIPE_KEY").valueFrom' <<<"$out")"
check "set-secret preserves existing secrets" \
  "2" \
  "$(jq '[.containerDefinitions[0].secrets[]|select(.name=="DB_USER" or .name=="JWT_SIGNING_KEY")]|length' <<<"$out")"
check "set-secret leaves env untouched" \
  "3" \
  "$(jq '[.containerDefinitions[0].environment[]]|length' <<<"$out")"
check "set-secret replaces same-name (no duplicate)" \
  "1" \
  "$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-secret JWT_SIGNING_KEY="$STRIPE_ARN" \
     | jq '[.containerDefinitions[0].secrets[]|select(.name=="JWT_SIGNING_KEY")]|length')"

# --- unset: remove a name from both environment and secrets -------------------
out=$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --unset LOG_LEVEL --unset DB_USER)
check "unset removes an env var" \
  "0" \
  "$(jq '[.containerDefinitions[0].environment[]|select(.name=="LOG_LEVEL")]|length' <<<"$out")"
check "unset removes a secret" \
  "0" \
  "$(jq '[.containerDefinitions[0].secrets[]|select(.name=="DB_USER")]|length' <<<"$out")"
check "unset leaves untouched env vars" \
  "2" \
  "$(jq '[.containerDefinitions[0].environment[]|select(.name=="HTTP_ADDR" or .name=="DB_HOST")]|length' <<<"$out")"

# --- no-op: setting a var to its current value registers nothing --------------
out=$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-env LOG_LEVEL=info)
check "no-op: identical value produces no-changes message" \
  "no changes — nothing to register" \
  "$out"

# --- no-op guard does NOT fire on a real change -------------------------------
out=$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-env LOG_LEVEL=debug)
check "real change bypasses no-op guard" \
  "debug" \
  "$(jq -r '.containerDefinitions[0].environment[]|select(.name=="LOG_LEVEL").value' <<<"$out")"

# --- validation: fail fast (exit 2) before any AWS call -----------------------
TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run >/dev/null 2>&1; rc=$?
check "no mutation flag exits 2" "2" "$rc"

TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-env NOEQUALS >/dev/null 2>&1; rc=$?
check "malformed --set-env exits 2" "2" "$rc"

TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-secret NOEQUALS >/dev/null 2>&1; rc=$?
check "malformed --set-secret exits 2" "2" "$rc"

# Must fail at validation BEFORE reading current/AWS, so no TASKDEF_INPUT here.
"$SCRIPT" --deploy --family hwh-match --set-env X=1 >/dev/null 2>&1; rc=$?
check "--deploy on a scheduled family exits 2" "2" "$rc"

# Flag that takes a value given as the last arg must not crash on set -u.
"$SCRIPT" --set-env >/dev/null 2>&1; rc=$?
check "flag with missing value exits 2" "2" "$rc"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
