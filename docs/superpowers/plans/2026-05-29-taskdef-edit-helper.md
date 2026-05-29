# taskdef-edit Helper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `scripts/taskdef-edit.sh`, a flag-driven helper that sets/unsets env vars and secret refs on a live ECS task definition and registers a new revision, replacing ad-hoc cutover-style `jq` surgery.

**Architecture:** A single Bash script. The risk surface — the `jq` mutation of the task-def JSON — is exercised by an AWS-free plain-bash test suite that drives the script through two seams: `TASKDEF_INPUT=<file>` (read the current task def from a fixture instead of calling AWS) and `--dry-run` (render the new task def to stdout, no prompt/register). Tasks 1–5 are test-driven and run entirely offline. Task 6 adds the AWS-touching glue (describe, secret-ARN resolution, diff, confirm, register, deploy), which is verified by the user against the account — never run locally (see the AWS constraint below).

**Tech Stack:** Bash, `jq`, AWS CLI v2 (ECS + Secrets Manager), GNU Make.

**Spec:** `docs/superpowers/specs/2026-05-29-taskdef-edit-helper-design.md`

> **AWS constraint (important):** This machine is logged into the company AWS account, and per the user's standing instruction we do **not** run AWS-mutating/plan commands locally. The Tasks 1–5 test suite is AWS-free by construction (uses `TASKDEF_INPUT` + `--dry-run` + `arn:` secret refs only) and is safe to run. The Task 6 verification (`describe-task-definition`, `register-task-definition`, `update-service`, `describe-secret`) must be run by the **user**, not the implementing agent.

---

## File structure

- **Create** `scripts/taskdef-edit.sh` — the helper. Single responsibility: render + (optionally) register a task-def revision from `--set-env` / `--set-secret` / `--unset` flags.
- **Create** `scripts/test/fixtures/taskdef.json` — a trimmed real `hwh-api` task-def revision used as test input. Includes the metadata fields the script must strip.
- **Create** `scripts/test/taskdef-edit.test.sh` — plain-bash, AWS-free test suite.
- **Modify** `Makefile` — add a `test-scripts` target (kept separate from the Go `test` target so it needs no AWS and no DB).

---

## Task 1: Test scaffold + fixture + `--set-env` upsert

Build the fixture, the test harness, the Makefile target, and the minimal script that renders `--set-env` changes via `TASKDEF_INPUT` + `--dry-run`. Start append-only, then add a replace test that forces dedupe-by-name.

**Files:**
- Create: `scripts/test/fixtures/taskdef.json`
- Create: `scripts/test/taskdef-edit.test.sh`
- Create: `scripts/taskdef-edit.sh`
- Modify: `Makefile:1` (`.PHONY`) and after `Makefile:92` (new target)

- [ ] **Step 1: Create the fixture**

`scripts/test/fixtures/taskdef.json`:

```json
{
  "family": "hwh-api",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "512",
  "memory": "1024",
  "executionRoleArn": "arn:aws:iam::111111111111:role/hwh-task-execution",
  "taskRoleArn": "arn:aws:iam::111111111111:role/hwh-task",
  "containerDefinitions": [
    {
      "name": "api",
      "image": "111111111111.dkr.ecr.us-east-1.amazonaws.com/hwh:abc1234",
      "essential": true,
      "command": ["serve"],
      "portMappings": [{ "containerPort": 8080, "protocol": "tcp" }],
      "environment": [
        { "name": "HTTP_ADDR", "value": ":8080" },
        { "name": "LOG_LEVEL", "value": "info" },
        { "name": "DB_HOST", "value": "db.example.rds.amazonaws.com" }
      ],
      "secrets": [
        { "name": "DB_USER", "valueFrom": "arn:aws:secretsmanager:us-east-1:111111111111:secret:rds!db-aaaa:username::" },
        { "name": "JWT_SIGNING_KEY", "valueFrom": "arn:aws:secretsmanager:us-east-1:111111111111:secret:hwh/jwt-signing-key-AbCdEf" }
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/aws/ecs/hwh/api",
          "awslogs-region": "us-east-1",
          "awslogs-stream-prefix": "api"
        }
      }
    }
  ],
  "taskDefinitionArn": "arn:aws:ecs:us-east-1:111111111111:task-definition/hwh-api:42",
  "revision": 42,
  "status": "ACTIVE",
  "requiresAttributes": [{ "name": "ecs.capability.execution-role-awslogs" }],
  "compatibilities": ["EC2", "FARGATE"],
  "registeredAt": "2026-05-20T00:00:00Z",
  "registeredBy": "arn:aws:iam::111111111111:user/someone"
}
```

- [ ] **Step 2: Create the test harness with the first (append) test**

`scripts/test/taskdef-edit.test.sh`:

```bash
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

printf '\n%d passed, %d failed\n' "$pass" "$fail"
((fail == 0))
```

- [ ] **Step 3: Run the test — verify it fails**

Run: `bash scripts/test/taskdef-edit.test.sh`
Expected: FAIL — `scripts/taskdef-edit.sh` does not exist (`No such file or directory`), assertions fail.

- [ ] **Step 4: Create the minimal script (append-only env)**

`scripts/taskdef-edit.sh`:

```bash
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

SET_ENV=() ; DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --family)   FAMILY="$2"; shift 2 ;;
    --set-env)  SET_ENV+=("$2"); shift 2 ;;
    --dry-run)  DRY_RUN=1; shift ;;
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

WORKDIR=$(mktemp -d); trap 'rm -rf "$WORKDIR"' EXIT

# Current revision. AWS fetch is added in a later task; for now require a file.
: "${TASKDEF_INPUT:?TASKDEF_INPUT must point to a task-def JSON (AWS fetch added later)}"
cp "$TASKDEF_INPUT" "$WORKDIR/current.json"

# Append the new env vars and strip register-rejected metadata fields.
jq --argjson envUp "$ENV_JSON" '
  .containerDefinitions[0].environment += $envUp
  | del(.taskDefinitionArn, .revision, .status, .requiresAttributes,
        .compatibilities, .registeredAt, .registeredBy)
' "$WORKDIR/current.json" > "$WORKDIR/new.json"

if ((DRY_RUN)); then cat "$WORKDIR/new.json"; exit 0; fi
```

Then make it executable: `chmod +x scripts/taskdef-edit.sh`

- [ ] **Step 5: Run the test — verify append passes**

Run: `bash scripts/test/taskdef-edit.test.sh`
Expected: PASS — `5 passed, 0 failed`.

- [ ] **Step 6: Add the replace test (forces dedupe)**

Append to `scripts/test/taskdef-edit.test.sh` before the summary `printf`:

```bash
# --- set-env: replace an existing var (no duplicate) --------------------------
out=$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-env LOG_LEVEL=debug)
check "set-env replaces existing value" \
  "debug" \
  "$(jq -r '.containerDefinitions[0].environment[]|select(.name=="LOG_LEVEL").value' <<<"$out")"
check "set-env leaves no duplicate" \
  "1" \
  "$(jq '[.containerDefinitions[0].environment[]|select(.name=="LOG_LEVEL")]|length' <<<"$out")"
```

- [ ] **Step 7: Run the test — verify replace fails**

Run: `bash scripts/test/taskdef-edit.test.sh`
Expected: FAIL — `set-env leaves no duplicate` is `2`, not `1` (append-only duplicates the key).

- [ ] **Step 8: Make the env upsert dedupe by name**

In `scripts/taskdef-edit.sh`, replace the `jq` render block with:

```bash
# Upsert env vars (replace same-name, else append); strip metadata fields.
jq --argjson envUp "$ENV_JSON" '
  ($envUp | map(.name)) as $en
  | .containerDefinitions[0].environment =
      (((.containerDefinitions[0].environment // [])
        | map(select(.name as $x | ($en | index($x)) == null))) + $envUp)
  | del(.taskDefinitionArn, .revision, .status, .requiresAttributes,
        .compatibilities, .registeredAt, .registeredBy)
' "$WORKDIR/current.json" > "$WORKDIR/new.json"
```

- [ ] **Step 9: Run the test — verify all pass**

Run: `bash scripts/test/taskdef-edit.test.sh`
Expected: PASS — `7 passed, 0 failed`.

- [ ] **Step 10: Add the Makefile target**

In `Makefile:1`, add `test-scripts` to the `.PHONY` list (append it to the end of the existing list).

After the `test` target (currently `Makefile:91-92`), add:

```makefile
# AWS-free shell tests for scripts/ (no DB, no AWS creds needed).
test-scripts:
	bash scripts/test/taskdef-edit.test.sh
```

- [ ] **Step 11: Run via make**

Run: `make test-scripts`
Expected: PASS — `7 passed, 0 failed`.

- [ ] **Step 12: Commit**

```bash
git add scripts/taskdef-edit.sh scripts/test/ Makefile
git commit -m "feat(scripts): taskdef-edit.sh env-var upsert + test scaffold"
```

---

## Task 2: `--set-secret` with `arn:` refs

Add secret-ref upsert. In this task only `arn:` refs are handled (used verbatim); name resolution via AWS is added in Task 6.

**Files:**
- Modify: `scripts/taskdef-edit.sh`
- Test: `scripts/test/taskdef-edit.test.sh`

- [ ] **Step 1: Add the failing test**

Append to `scripts/test/taskdef-edit.test.sh` before the summary `printf`:

```bash
# --- set-secret: add a ref with an arn: value (verbatim) ----------------------
STRIPE_ARN='arn:aws:secretsmanager:us-east-1:111111111111:secret:hwh/stripe-key-AbCdEf'
out=$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-secret STRIPE_KEY="$STRIPE_ARN")
check "set-secret adds ref verbatim" \
  "$STRIPE_ARN" \
  "$(jq -r '.containerDefinitions[0].secrets[]|select(.name=="STRIPE_KEY").valueFrom' <<<"$out")"
check "set-secret preserves existing secrets" \
  "2" \
  "$(jq '[.containerDefinitions[0].secrets[]|select(.name=="DB_USER" or .name=="JWT_SIGNING_KEY")]|length' <<<"$out")"
check "set-secret replaces same-name (no duplicate)" \
  "1" \
  "$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-secret JWT_SIGNING_KEY="$STRIPE_ARN" \
     | jq '[.containerDefinitions[0].secrets[]|select(.name=="JWT_SIGNING_KEY")]|length')"
```

- [ ] **Step 2: Run the test — verify it fails**

Run: `make test-scripts`
Expected: FAIL — `--set-secret` is an unknown argument (exit 2 / empty output), secret assertions fail.

- [ ] **Step 3: Implement `--set-secret`**

In `scripts/taskdef-edit.sh`, change the array declaration line from:

```bash
SET_ENV=() ; DRY_RUN=0
```

to:

```bash
SET_ENV=() ; SET_SECRET=() ; DRY_RUN=0
```

Add a case to the arg-parse `while` loop (before the `*)` catch-all):

```bash
    --set-secret) SET_SECRET+=("$2"); shift 2 ;;
```

Add a secret-ref resolver function just below the `export AWS_PROFILE ...` line:

```bash
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
```

After the `ENV_JSON` build block, add a `SEC_JSON` build block:

```bash
# Build a JSON array of {name,valueFrom} from the --set-secret pairs.
SEC_JSON='[]'
if ((${#SET_SECRET[@]})); then
  for nv in "${SET_SECRET[@]}"; do
    name=${nv%%=*}; ref=${nv#*=}
    valueFrom=$(resolve_secret_ref "$ref")
    SEC_JSON=$(jq -c --arg n "$name" --arg v "$valueFrom" '. + [{name:$n, valueFrom:$v}]' <<<"$SEC_JSON")
  done
fi
```

Replace the `jq` render block to also upsert secrets:

```bash
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
```

- [ ] **Step 4: Run the test — verify it passes**

Run: `make test-scripts`
Expected: PASS — `10 passed, 0 failed`.

- [ ] **Step 5: Commit**

```bash
git add scripts/taskdef-edit.sh scripts/test/taskdef-edit.test.sh
git commit -m "feat(scripts): taskdef-edit.sh --set-secret (arn refs)"
```

---

## Task 3: `--unset` removes from env and secrets

**Files:**
- Modify: `scripts/taskdef-edit.sh`
- Test: `scripts/test/taskdef-edit.test.sh`

- [ ] **Step 1: Add the failing test**

Append to `scripts/test/taskdef-edit.test.sh` before the summary `printf`:

```bash
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
```

- [ ] **Step 2: Run the test — verify it fails**

Run: `make test-scripts`
Expected: FAIL — `--unset` is an unknown argument (exit 2 / empty output).

- [ ] **Step 3: Implement `--unset`**

In `scripts/taskdef-edit.sh`, change the array declaration line to add `UNSET`:

```bash
SET_ENV=() ; SET_SECRET=() ; UNSET=() ; DRY_RUN=0
```

Add a case to the arg-parse loop (before `*)`):

```bash
    --unset) UNSET+=("$2"); shift 2 ;;
```

After the `SEC_JSON` build block, add an `UNSET_JSON` build block:

```bash
# Build a JSON array of names to remove from both environment and secrets.
UNSET_JSON='[]'
if ((${#UNSET[@]})); then
  UNSET_JSON=$(printf '%s\n' "${UNSET[@]}" | jq -R . | jq -cs .)
fi
```

Replace the `jq` render block so the drop-sets include unset names:

```bash
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
```

- [ ] **Step 4: Run the test — verify it passes**

Run: `make test-scripts`
Expected: PASS — `13 passed, 0 failed`.

- [ ] **Step 5: Commit**

```bash
git add scripts/taskdef-edit.sh scripts/test/taskdef-edit.test.sh
git commit -m "feat(scripts): taskdef-edit.sh --unset (env + secrets)"
```

---

## Task 4: No-op guard (order-insensitive)

If the rendered env+secrets match current (ignoring order), print `no changes — nothing to register` and exit 0 before the dry-run output / register.

**Files:**
- Modify: `scripts/taskdef-edit.sh`
- Test: `scripts/test/taskdef-edit.test.sh`

- [ ] **Step 1: Add the failing test**

Append to `scripts/test/taskdef-edit.test.sh` before the summary `printf`:

```bash
# --- no-op: setting a var to its current value registers nothing --------------
out=$(TASKDEF_INPUT="$FIXTURE" "$SCRIPT" --dry-run --set-env LOG_LEVEL=info)
check "no-op change is detected" \
  "no changes — nothing to register" \
  "$out"
```

- [ ] **Step 2: Run the test — verify it fails**

Run: `make test-scripts`
Expected: FAIL — output is the full rendered JSON, not `no changes — nothing to register`. (The upsert reorders `LOG_LEVEL` to the end but its value is unchanged.)

- [ ] **Step 3: Implement the no-op guard**

In `scripts/taskdef-edit.sh`, between the `jq ... > "$WORKDIR/new.json"` render block and the `if ((DRY_RUN))` line, insert:

```bash
# No-op guard: compare env+secrets as order-insensitive name->value maps.
# The upsert reorders entries, so a naive byte-compare would misread that as a
# change; -S sorts keys for a stable comparison.
maps() {
  jq -S '{
    env: ((.containerDefinitions[0].environment // []) | map({(.name): .value})    | add // {}),
    sec: ((.containerDefinitions[0].secrets     // []) | map({(.name): .valueFrom}) | add // {})
  }' "$1"
}
if [[ "$(maps "$WORKDIR/current.json")" == "$(maps "$WORKDIR/new.json")" ]]; then
  echo "no changes — nothing to register"
  exit 0
fi
```

- [ ] **Step 4: Run the test — verify it passes**

Run: `make test-scripts`
Expected: PASS — `14 passed, 0 failed`.

- [ ] **Step 5: Commit**

```bash
git add scripts/taskdef-edit.sh scripts/test/taskdef-edit.test.sh
git commit -m "feat(scripts): taskdef-edit.sh order-insensitive no-op guard"
```

---

## Task 5: Argument validation & error paths

Fail fast (exit 2), before any AWS call, on: no mutation flag, malformed `--set-env`/`--set-secret`, and `--deploy` for a non-`hwh-api` family. All testable offline.

**Files:**
- Modify: `scripts/taskdef-edit.sh`
- Test: `scripts/test/taskdef-edit.test.sh`

- [ ] **Step 1: Add the failing tests**

Append to `scripts/test/taskdef-edit.test.sh` before the summary `printf`:

```bash
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
```

- [ ] **Step 2: Run the test — verify it fails**

Run: `make test-scripts`
Expected: FAIL — malformed pairs currently render with an odd name/value instead of exiting 2; `--deploy`/`--yes` are unknown args; no mutation currently renders the unchanged def.

- [ ] **Step 3: Implement validation**

In `scripts/taskdef-edit.sh`, add a `usage` function just below the `set -euo pipefail` line:

```bash
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
```

Add `--deploy`/`--yes` to the array declaration and arg-parse loop. Change the declaration line to:

```bash
SET_ENV=() ; SET_SECRET=() ; UNSET=() ; DEPLOY=0 ; YES=0 ; DRY_RUN=0
```

Add these cases to the parse loop (before `*)`):

```bash
    --deploy)   DEPLOY=1; shift ;;
    --yes|-y)   YES=1; shift ;;
    -h|--help)  usage; exit 0 ;;
```

Immediately after the arg-parse `while` loop ends, add the validation block:

```bash
if ((${#SET_ENV[@]} + ${#SET_SECRET[@]} + ${#UNSET[@]} == 0)); then
  echo "error: at least one of --set-env / --set-secret / --unset is required" >&2
  usage; exit 2
fi
if ((DEPLOY)) && [[ "$FAMILY" != "hwh-api" ]]; then
  echo "error: --deploy only applies to hwh-api (the only ECS service); '$FAMILY' is scheduled and picks up :LATEST on next firing" >&2
  exit 2
fi
```

In the `ENV_JSON` build loop, add a format check as the first line inside `for kv ...`:

```bash
    [[ "$kv" == *=* ]] || { echo "error: --set-env expects KEY=VALUE, got '$kv'" >&2; exit 2; }
```

In the `SEC_JSON` build loop, add as the first line inside `for nv ...`:

```bash
    [[ "$nv" == *=* ]] || { echo "error: --set-secret expects NAME=REF, got '$nv'" >&2; exit 2; }
```

- [ ] **Step 4: Run the test — verify it passes**

Run: `make test-scripts`
Expected: PASS — `18 passed, 0 failed`.

- [ ] **Step 5: Commit**

```bash
git add scripts/taskdef-edit.sh scripts/test/taskdef-edit.test.sh
git commit -m "feat(scripts): taskdef-edit.sh argument validation"
```

---

## Task 6: AWS glue — fetch, diff, confirm, register, deploy

Wire up the AWS-touching path: fetch the current revision when `TASKDEF_INPUT` is unset, print a change diff, prompt for confirmation, register, and roll/print next steps. This path has no offline unit test (the tests cover the render via `--dry-run`); it is verified by the **user** against the account.

**Files:**
- Modify: `scripts/taskdef-edit.sh`

- [ ] **Step 1: Add the AWS describe fallback**

In `scripts/taskdef-edit.sh`, replace the current-revision block:

```bash
# Current revision. AWS fetch is added in a later task; for now require a file.
: "${TASKDEF_INPUT:?TASKDEF_INPUT must point to a task-def JSON (AWS fetch added later)}"
cp "$TASKDEF_INPUT" "$WORKDIR/current.json"
```

with:

```bash
# Current revision: from TASKDEF_INPUT (tests) or live from AWS.
if [[ -n "${TASKDEF_INPUT:-}" ]]; then
  cp "$TASKDEF_INPUT" "$WORKDIR/current.json"
else
  aws ecs describe-task-definition --task-definition "$FAMILY" \
    --query 'taskDefinition' > "$WORKDIR/current.json"
fi
```

- [ ] **Step 2: Add a tool precondition check**

In `scripts/taskdef-edit.sh`, just above the `WORKDIR=$(mktemp -d)` line, add:

```bash
# jq is always needed; aws only when we'll actually call it (live fetch or any
# register/deploy). This keeps `--dry-run` + TASKDEF_INPUT (the test path)
# runnable without the aws CLI installed.
command -v jq >/dev/null 2>&1 || { echo "error: 'jq' not found on PATH" >&2; exit 1; }
if ((! DRY_RUN)) || [[ -z "${TASKDEF_INPUT:-}" ]]; then
  command -v aws >/dev/null 2>&1 || { echo "error: 'aws' not found on PATH" >&2; exit 1; }
fi
```

This check sits after the `ENV_JSON`/`SEC_JSON`/`UNSET_JSON` build blocks, so the offline test path (`--dry-run` with `TASKDEF_INPUT` set and `arn:` secret refs) never requires `aws`.

- [ ] **Step 3: Add the diff + confirm + register + rollout block**

In `scripts/taskdef-edit.sh`, replace the dry-run line:

```bash
if ((DRY_RUN)); then cat "$WORKDIR/new.json"; exit 0; fi
```

with the full tail of the script:

```bash
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
  printf 'Register new revision for %s? [y/N] ' "$FAMILY" > /dev/tty
  read -r reply < /dev/tty
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
```

- [ ] **Step 4: Re-run the offline suite (regression check)**

Run: `make test-scripts`
Expected: PASS — `18 passed, 0 failed` (the AWS path is gated behind a live run; `--dry-run`/`TASKDEF_INPUT` paths are unchanged).

- [ ] **Step 5: Lint the script**

Run: `shellcheck scripts/taskdef-edit.sh` (if `shellcheck` is installed; otherwise skip).
Expected: no errors. Fix any warnings (common: quote `$NEW_ARN`, already handled).

- [ ] **Step 6: USER verifies the AWS path (do NOT run locally as the agent)**

Hand these to the user to run against the account. The agent must not run AWS commands locally.

```bash
# read-only: render the live api task def with a proposed change, register nothing
scripts/taskdef-edit.sh --set-env TASKDEF_EDIT_SMOKE=1 --dry-run | jq '.containerDefinitions[0].environment'

# full path, but answer "N" at the prompt to confirm the diff prints and nothing registers
scripts/taskdef-edit.sh --set-env TASKDEF_EDIT_SMOKE=1
```

Expected: the dry-run prints the env array including `TASKDEF_EDIT_SMOKE=1`; the second prints the diff line `ENV    + TASKDEF_EDIT_SMOKE=1`, prompts, and on `N` prints `aborted — nothing registered` with no new revision.

- [ ] **Step 7: Commit**

```bash
git add scripts/taskdef-edit.sh
git commit -m "feat(scripts): taskdef-edit.sh AWS glue (fetch, diff, confirm, register, deploy)"
```

---

## Task 7: Documentation

Point future maintainers at the helper from the cutover script and (if present) the ops docs, so the next person adds env/secrets the easy way.

**Files:**
- Modify: `scripts/cutover-register-taskdef.sh`

- [ ] **Step 1: Add a pointer comment to the cutover script**

In `scripts/cutover-register-taskdef.sh`, under the top comment block (after the line describing it as a one-time flip, around line 14), add:

```bash
# For ONGOING env/secret changes (not this one-time migration), use
# scripts/taskdef-edit.sh instead — it does the same describe->jq->register
# flip generically, with a diff + confirm gate. See
# docs/superpowers/specs/2026-05-29-taskdef-edit-helper-design.md
```

- [ ] **Step 2: Commit**

```bash
git add scripts/cutover-register-taskdef.sh
git commit -m "docs(scripts): point cutover script at taskdef-edit.sh for ongoing changes"
```

---

## Self-review notes

- **Spec coverage:** scope/task-def-wiring-only (Tasks 1–3, 6); `arn:` vs name secret resolution (Task 2 + Task 6 step 1/3); register-only default + `--deploy` (Task 6); diff + `y/N` confirm with `--yes` (Task 6); per-family rollout messaging (Task 6); no-op guard order-insensitive (Task 4); error handling (Task 5 + Task 6 step 2); testing approach A with `TASKDEF_INPUT`/`--dry-run` seams and Makefile target (Tasks 1–5); files list (all tasks). All spec sections map to a task.
- **`--dry-run` / `TASKDEF_INPUT`:** these are the test seams named in the spec; `--dry-run` also doubles as a human preview. Confirmed they do not replace the `y/N` gate (Task 6 keeps the prompt on the non-dry-run path).
- **Type/name consistency:** `ENV_JSON`/`SEC_JSON`/`UNSET_JSON`, `SET_ENV`/`SET_SECRET`/`UNSET`, `resolve_secret_ref`, `maps`, `WORKDIR/current.json`, `WORKDIR/new.json`, `FAMILY`/`CLUSTER` are used consistently across tasks. The `jq` drop-set names `$en`/`$sn`/`$edrop`/`$sdrop` are consistent from Task 2 onward.
- **macOS Bash note:** array expansions are guarded with `((${#arr[@]}))` so they are safe under `set -u` on the system Bash 3.2.
