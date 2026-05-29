# `scripts/taskdef-edit.sh` — task-def env/secret edit helper

Date: 2026-05-29
Status: Approved (design)

## Problem

Adding a new environment variable or secret to a running ECS task definition is
currently a manual, error-prone chore. The cause is split-brain ownership of the
`api` container definition:

- `terraform/prod/ecs_api.tf` declares the env/secret lists (`api_env_vars`,
  `api_secrets`) but the task def carries `lifecycle { ignore_changes =
  [container_definitions] }` (`ecs_api.tf:74`). After the first apply, Terraform
  never pushes env/secret changes again — editing the locals and running
  `terraform apply` is a no-op on the live task def.
- The app deploy pipeline (`ci/buildspec-app.yml:74-80`) reads the **live** task
  def, `jq`'s in **only the image**, and registers a new revision. Env and
  secrets just pass through whatever is already live.

So once bootstrapped, the *live task def* is the source of truth for env/secrets,
and nothing in the normal PR → pipeline flow edits it. The only way to change
env/secrets today is bespoke `describe → jq → register → update-service` surgery
of the kind in `scripts/cutover-register-taskdef.sh` — re-authored by hand each
time, and repeated per family.

This helper removes the per-change `jq` authoring while preserving the deliberate,
gated rollout discipline of the cutover script.

## Scope

**In scope — task-def wiring only:** set/unset plain env vars and secret
references on a live task def and register a new revision; optionally roll the
`hwh-api` service.

**Out of scope:**

- Secret *existence* and IAM read grants — Terraform keeps owning these via
  `secret_names` in `terraform/prod/secrets.tf:2` and the `for_each` grant in
  `terraform/prod/iam.tf:31`.
- Seeding a new secret's *value* — done once, manually, with
  `aws secretsmanager put-secret-value` (as documented in `secrets.tf:23`).
- Terraform, migrations, image builds.

The flow for adding a brand-new secret is therefore: (1) add its name to
`secret_names` and `terraform apply` (creates the Secrets Manager entry + IAM
grant), (2) `put-secret-value` once, (3) `taskdef-edit.sh --set-secret …` to wire
the reference into the task def. This helper owns only step 3.

## Interface

```
scripts/taskdef-edit.sh [--family hwh-api] \
  [--set-env  KEY=VALUE]...   \
  [--set-secret NAME=REF]...  \
  [--unset NAME]...           \
  [--deploy] [--yes] [--dry-run]
```

| Flag | Meaning |
|------|---------|
| `--family NAME` | Target task-def family. Defaults to `hwh-api`. Must be passed explicitly to touch a scheduled family. |
| `--set-env KEY=VALUE` | Repeatable. Upsert a plain env var (replace if `KEY` exists, else append). |
| `--set-secret NAME=REF` | Repeatable. Upsert a secret ref. See **Secret ref resolution**. |
| `--unset NAME` | Repeatable. Remove `NAME` from **both** `environment` and `secrets`. |
| `--deploy` | After registering, roll `hwh-api` via `update-service`. Errors for a non-`hwh-api` family. |
| `--yes` | Skip the interactive confirmation prompt (CI / scripted use). |
| `--dry-run` | Render the new task-def JSON to stdout and exit — no prompt, no register, no deploy. Test seam and human preview. |

At least one mutation flag (`--set-env` / `--set-secret` / `--unset`) is required;
otherwise the script prints usage and exits non-zero.

**Env-overridable defaults (same convention as the cutover script):**
`FAMILY=hwh-api`, `CLUSTER=hwh-cluster`, `AWS_PROFILE=servant`,
`AWS_DEFAULT_REGION=us-east-1`.

### Secret ref resolution

For each `--set-secret NAME=REF`:

- If `REF` starts with `arn:` it is used verbatim as `valueFrom`. This supports
  JSON-key refs such as `<master-arn>:password::`.
- Otherwise `REF` is treated as a Secrets Manager secret-id (e.g. `hwh/api-key`)
  and resolved to its ARN via
  `aws secretsmanager describe-secret --secret-id REF --query ARN --output text`.
  An unknown name fails here, before anything is registered.

This keeps the common "new app secret" case a short, readable line
(`--set-secret STRIPE_KEY=hwh/stripe-key`) while still allowing full ARNs and
JSON-key refs.

## Behavior / flow

1. **Preconditions** — `set -euo pipefail`; require `aws` + `jq` on `PATH`;
   export `AWS_PROFILE` / `AWS_DEFAULT_REGION`; parse flags; require ≥1 mutation.
2. **Resolve secret refs** — resolve every non-`arn:` `--set-secret` REF to an ARN
   (see above). Fails fast on a bad name.
3. **Fetch current revision** — `aws ecs describe-task-definition
   --task-definition $FAMILY --query 'taskDefinition'` into `current.json` in a
   `mktemp -d` workdir (cleaned via `trap '… rm -rf' EXIT`). If `TASKDEF_INPUT`
   (test seam) is set, read that file instead of calling AWS.
4. **Apply mutations — one `jq` pass** over `containerDefinitions[0]`:
   - env upserts: drop any existing `environment` entry with the same `name`,
     then append `{name, value}`.
   - secret upserts: drop any existing `secrets` entry with the same `name`, then
     append `{name, valueFrom}` (resolved ARN).
   - unsets: filter `name` out of **both** `environment` and `secrets`.
   - `del(.taskDefinitionArn, .revision, .status, .requiresAttributes,
     .compatibilities, .registeredAt, .registeredBy)` — same del-list as
     `buildspec-app.yml:78` and the cutover script. A comment notes the
     `enableFaultInjection` caveat (add to the del-list if a newer AWS CLI
     rejects it on register).
5. **No-op guard** — if the rendered `environment` + `secrets` match current,
   print `no changes — nothing to register` and exit `0`. The comparison is
   **order-insensitive** (each list compared as a `name → value`/`name →
   valueFrom` map), because an upsert of an existing key is implemented as
   drop-then-append and would otherwise reorder entries and read as a spurious
   change. Avoids spawning duplicate revisions on a re-run (idempotent).
6. **`--dry-run`** — if set, print the rendered new task-def JSON to stdout and
   exit `0` here (before diff/prompt/register).
7. **Diff + confirm** — print a concise change summary against the current
   revision, e.g.:
   ```
   ENV    + TEI_TIMEOUT=30s
   ENV    ~ LOG_LEVEL: info -> debug
   SECRET + STRIPE_KEY -> arn:…:secret:hwh/stripe-key-AbCdEf
   ENV    - OLD_FLAG
   ```
   then prompt `Register new revision for $FAMILY? [y/N] ` read from `/dev/tty`
   (works with piped stdin). `--yes` skips the prompt. Anything but `y`/`Y`
   aborts with **no** AWS mutation.
   - Printing is safe: `secrets` entries carry only `valueFrom` *references*
     (ARNs), never secret material. Env values are config (by convention secrets
     go through Secrets Manager) and are shown verbatim.
8. **Register** — `aws ecs register-task-definition --cli-input-json
   file://new.json --query 'taskDefinition.taskDefinitionArn' --output text` →
   `NEW_ARN`; echo it.
9. **Rollout** — depends on family:
   - `--deploy` + `hwh-api`: `aws ecs update-service --cluster $CLUSTER --service
     $FAMILY --task-definition $NEW_ARN`.
   - `--deploy` + non-api family: error — scheduled families
     (`hwh-scrape-*`, `hwh-match`) are not ECS services.
   - no `--deploy`, `hwh-api`: print the exact `update-service` command as a
     next-step.
   - no `--deploy`, scheduled family: print a note that the family picks up the
     new revision on its next EventBridge firing (`:LATEST`,
     per `buildspec-app.yml:84`) — no `update-service` needed.

### Per-family rollout semantics (why the review gate is pre-register)

- `hwh-api` — the new revision is inert until `update-service`. Register-only
  leaves a real window to inspect before `--deploy`.
- `hwh-scrape-*` / `hwh-match` — EventBridge-scheduled against `:LATEST`; the next
  firing uses the newest revision, so **registering is effectively deploying**.
  There is no post-register gate for these, which is exactly why the review
  (diff + confirm) happens **before** `register-task-definition` runs.

## Error handling

Each of the following fails fast with a one-line message and non-zero exit,
**before** any `register`:

- `aws` or `jq` missing from `PATH`.
- No mutation flag supplied.
- Malformed `--set-env` (no `=`).
- Unresolvable `--set-secret` name.
- `--deploy` with a non-`hwh-api` family.

`set -euo pipefail` throughout; `mktemp -d` + `trap 'rm -rf "$WORKDIR"' EXIT` for
the workdir (mirrors `scripts/cutover-register-taskdef.sh`).

## Testing

The repo has no shell-test harness (Go-only; existing `scripts/*.sh` are
untested). The genuine risk surface is the `jq` mutation; the AWS calls are thin
glue.

- Add one **plain-bash** test (no new `bats` dependency), e.g.
  `scripts/test/taskdef-edit.test.sh`, exercising the mutation logic through the
  two test seams — `TASKDEF_INPUT=<fixture>` (skip the AWS describe) and
  `--dry-run` (render to stdout, no prompt/register). It feeds a committed
  fixture (`scripts/test/fixtures/taskdef.json`, a trimmed real `hwh-api`
  revision) through each case and asserts the resulting `environment` / `secrets`
  with `jq`:
  - `--set-env` for a new key (append).
  - `--set-env` for an existing key (replace, no duplicate).
  - `--set-secret` with an `arn:` REF (verbatim, no AWS).
  - `--unset` removes a name from both `environment` and `secrets`.
  - no-op detection (identical render → `no changes`, exit 0).
- Secret *name resolution* (the one AWS-touching branch) is **not** unit-tested;
  verified manually against the real account.
- Wire the test into the Makefile as a `test-scripts` target (kept separate from
  the Go `test` target so it does not require AWS or alter the existing target).

The test asserts only on env/secrets content, not full-JSON equality, so it
won't be brittle against unrelated task-def fields.

## Files

- `scripts/taskdef-edit.sh` — the helper (new).
- `scripts/test/taskdef-edit.test.sh` — plain-bash transform test (new).
- `scripts/test/fixtures/taskdef.json` — trimmed `hwh-api` task-def fixture (new).
- `Makefile` — add `test-scripts` target (edit).

## Example sessions

Add a plain env var to the api and roll it:
```
scripts/taskdef-edit.sh --set-env TEI_TIMEOUT=30s --deploy
```

Add a new integration (env var + secret) as one revision, register only:
```
# 1. terraform: add "stripe-key" to secret_names, apply
# 2. aws secretsmanager put-secret-value --secret-id hwh/stripe-key --secret-string "$KEY"
# 3.
scripts/taskdef-edit.sh \
  --set-env STRIPE_BASE_URL=https://api.stripe.com \
  --set-secret STRIPE_KEY=hwh/stripe-key
# review the diff, answer y; then run the printed update-service command when ready
```

Change a scheduled family (picked up next firing, no update-service):
```
scripts/taskdef-edit.sh --family hwh-match --set-env MATCH_BATCH=500
```

Preview only, no mutation:
```
scripts/taskdef-edit.sh --set-env LOG_LEVEL=debug --dry-run | jq '.containerDefinitions[0].environment'
```
