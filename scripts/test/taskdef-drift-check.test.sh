#!/usr/bin/env bash
# Plain-bash unit tests for scripts/taskdef-drift-check.sh — AWS-free.
# Drives the script via DRIFT_INPUT_DIR (skip AWS describe; read
# <family>.json from a fixture dir). Run: make test-scripts
set -uo pipefail   # NOT -e: we capture non-zero exit codes from the script

HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT="$HERE/../taskdef-drift-check.sh"
FIXTURES="$HERE/fixtures/drift"
pass=0 fail=0

check() { # desc expected actual
  if [[ "$2" == "$3" ]]; then
    pass=$((pass + 1)); printf 'ok   - %s\n' "$1"
  else
    fail=$((fail + 1)); printf 'FAIL - %s\n      expected: [%s]\n      actual:   [%s]\n' "$1" "$2" "$3"
  fi
}

contains() { # desc needle haystack
  if [[ "$3" == *"$2"* ]]; then
    pass=$((pass + 1)); printf 'ok   - %s\n' "$1"
  else
    fail=$((fail + 1)); printf 'FAIL - %s\n      expected to contain: [%s]\n      actual: [%s]\n' "$1" "$2" "$3"
  fi
}

run() { DRIFT_INPUT_DIR="$FIXTURES" "$SCRIPT" "$@" 2>&1; }

# --- a family carrying every reference name passes ----------------------------
out=$(run --family hwh-good); rc=$?
check "superset family exits 0" "0" "$rc"
contains "superset family reported ok" "hwh-good" "$out"

# --- an extra var on the scheduled family is not drift ------------------------
check "extra scheduled-only var does not fail" "0" "$rc"

# --- a family missing env vars fails and names them ---------------------------
out=$(run --family hwh-missing-env); rc=$?
check "missing env vars exits 1" "1" "$rc"
contains "names the first missing env var" "EMAIL_SENDER" "$out"
contains "names the second missing env var" "APP_BASE_URL" "$out"

# --- a family missing a secret fails and names it -----------------------------
out=$(run --family hwh-missing-secret); rc=$?
check "missing secret exits 1" "1" "$rc"
contains "names the missing secret" "JWT_SIGNING_KEY" "$out"

# --- one bad family among several fails the whole run -------------------------
out=$(run --family hwh-good --family hwh-missing-env); rc=$?
check "one bad family fails the run" "1" "$rc"
contains "still names the missing var" "EMAIL_SENDER" "$out"

# --- the failure output tells the operator how to repair it -------------------
out=$(run --family hwh-missing-env); rc=$?
contains "suggests taskdef-edit.sh" "taskdef-edit.sh" "$out"
contains "suggests the offending family" "--family hwh-missing-env" "$out"

# --- a custom reference family is honoured ------------------------------------
out=$(run --reference hwh-good --family hwh-missing-env); rc=$?
contains "custom reference is used" "SCHEDULED_ONLY_EXTRA" "$out"

# --- an unknown family is an error, not a silent pass -------------------------
out=$(run --family hwh-does-not-exist); rc=$?
check "unknown family exits non-zero" "1" "$rc"

# --- credentials: the servant profile default must not leak into CodeBuild ----
# CodeBuild authenticates with the build role's ambient credentials and has no
# named profile, so exporting AWS_PROFILE=servant there fails every build with
# "The config profile (servant) could not be found". Drive the real AWS path
# (no DRIFT_INPUT_DIR) with a stub `aws` that reports what it was handed.
STUB_DIR=$(mktemp -d)
cat >"$STUB_DIR/aws" <<'STUB'
#!/usr/bin/env bash
echo "AWS_PROFILE=[${AWS_PROFILE:-}] AWS_DEFAULT_REGION=[${AWS_DEFAULT_REGION:-}]" >&2
exit 1
STUB
chmod +x "$STUB_DIR/aws"

out=$(PATH="$STUB_DIR:$PATH" CODEBUILD_BUILD_ID=hwh-app:1234 AWS_REGION=us-east-1 \
  env -u AWS_PROFILE -u AWS_DEFAULT_REGION "$SCRIPT" --family hwh-good 2>&1)
contains "no servant profile under CodeBuild" "AWS_PROFILE=[]" "$out"
contains "region still resolved under CodeBuild" "AWS_DEFAULT_REGION=[us-east-1]" "$out"

out=$(PATH="$STUB_DIR:$PATH" env -u AWS_PROFILE -u AWS_DEFAULT_REGION -u CODEBUILD_BUILD_ID \
  "$SCRIPT" --family hwh-good 2>&1)
contains "servant profile still defaults locally" "AWS_PROFILE=[servant]" "$out"

out=$(PATH="$STUB_DIR:$PATH" AWS_PROFILE=other env -u CODEBUILD_BUILD_ID \
  "$SCRIPT" --family hwh-good 2>&1)
contains "explicit profile is not overridden" "AWS_PROFILE=[other]" "$out"

rm -rf "$STUB_DIR"

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
