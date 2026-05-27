# Here's What's Happening

A custom event calendar based on your interests.

See [docs/superpowers/specs/2026-05-19-event-calendar-design.md](docs/superpowers/specs/2026-05-19-event-calendar-design.md) for the v1 design.

## Local dev quickstart — backend foundation

Prerequisites: Go 1.24+, Docker, `sqlc` (`go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0`).

```bash
cp .env.example .env
# Start Postgres + pgvector (creates appdb and appdb_test)
make db-up

# Apply migrations to both databases
make migrate
make migrate-test

# Run the test suite (integration tests against appdb_test)
make test

# Run the server
make run
# In another shell:
curl http://localhost:8080/healthz
```

### Try the auth flow

```bash
ACCESS=$(curl -s -X POST http://localhost:8080/auth/signup \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"hunter22"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])')

curl http://localhost:8080/me -H "Authorization: Bearer $ACCESS"
curl -X POST http://localhost:8080/me/interests \
  -H "Authorization: Bearer $ACCESS" \
  -H 'Content-Type: application/json' \
  -d '{"value":"Indie Rock"}'
curl http://localhost:8080/me/interests -H "Authorization: Bearer $ACCESS"
```

## Event ingest quickstart

```bash
# Start ElasticMQ (local SQS) alongside Postgres
make queue-up

# Set your Ticketmaster API key (free, https://developer.ticketmaster.com)
export TICKETMASTER_API_KEY=<your-key>
export TICKETMASTER_CITY="New York"

# Run a one-shot scrape (publishes EventMessage records to events-queue)
./app scrape events --source=ticketmaster

# Run the server with the ingest consumer enabled. The consumer drains
# events-queue and upserts into Postgres.
make run
# In another shell:
docker exec hwh_postgres psql -U app -d appdb -c "SELECT count(*) FROM events;"
```

The ingest pipeline is decoupled: scraping and serving are independent
processes that communicate through the queue. You can run the scraper without
the server (messages queue up) or the server without the scraper (consumer
sits idle, long-polling).

## Spotify integration quickstart

```bash
# Prereqs: register a Spotify app at https://developer.spotify.com/dashboard
# Redirect URI: http://localhost:8080/integrations/spotify/callback
# Copy Client ID + Secret into .env
export SPOTIFY_CLIENT_ID=<your-id>
export SPOTIFY_CLIENT_SECRET=<your-secret>
export SPOTIFY_REDIRECT_URI=http://localhost:8080/integrations/spotify/callback

# Generate an at-rest encryption key for Spotify tokens
openssl rand -base64 32   # paste into .env as SPOTIFY_TOKEN_ENC_KEY

make db-up && make queue-up
make migrate && make migrate-test
make run    # starts api + events consumer + interests consumer
```

### Connect a user

1. Sign up + log in via the auth flow (Local dev quickstart).
2. Visit `http://localhost:8080/integrations/spotify/connect` in a browser
   with your access token in the `Authorization` header (use a REST client
   like Postman, or wrap in a small HTML form).
3. Spotify will redirect back to `/integrations/spotify/callback` — the
   server stores the encrypted tokens and immediately publishes one
   InterestMessage to the interests-queue. The consumer drains it.
4. Verify:

```bash
docker exec hwh_postgres psql -U app -d appdb -c \
  "SELECT kind, value, weight FROM user_interests \
   WHERE kind LIKE 'spotify%' ORDER BY weight DESC LIMIT 10;"
```

### Periodic scrape

```bash
./app scrape spotify   # iterates all connected users, publishes fresh InterestMessages
```

## Match-job quickstart

```bash
# Start the TEI sidecar (BAAI/bge-small-en-v1.5)
make tei-up
# First run downloads the model; takes ~2 minutes. Subsequent runs are fast.

# Verify TEI is healthy
curl -s http://localhost:8081/health

# Run the match-job
./app match
# Steps it runs:
#  1. Embed any events whose embedding column is NULL
#  2. Embed any users whose interests changed since last embedding
#  3. Score every (user, event) pair; upsert above-threshold matches
#  4. Archive events not seen in the last 7 days
```

### Tuning weights

Match weights live in the `match_config` table; change them with SQL and the
next `./app match` picks them up — no rebuild needed.

```sql
UPDATE match_config SET value = '0.7'::jsonb WHERE key = 'w_string';
UPDATE match_config SET value = '0.3'::jsonb WHERE key = 'w_embedding';
```

### Inspect a user's matches

```bash
docker exec hwh_postgres psql -U app -d appdb -c "
  SELECT e.title, m.score, m.score_breakdown
  FROM user_event_match m
  JOIN events e ON e.id = m.event_id
  WHERE m.user_id = (SELECT id FROM users WHERE email = 'you@example.com')
  ORDER BY m.score DESC, e.starts_at ASC
  LIMIT 20;
"
```

## Calendar API + iCal feed quickstart

```bash
# Make sure ICAL_BASE_URL is set in .env (default http://localhost:8080)
make db-up && make run
```

### Read your matched calendar

```bash
ACCESS=...  # JWT from /auth/login
curl -s -H "Authorization: Bearer $ACCESS" \
  "http://localhost:8080/me/calendar?from=2026-05-20&to=2026-08-01" \
  | python3 -m json.tool | head -40
```

### Subscribe via your calendar app

```bash
# Generate a token — the URL is returned exactly once.
ACCESS=...
curl -s -X POST -H "Authorization: Bearer $ACCESS" \
  http://localhost:8080/me/ical-token
# → {"url":"http://localhost:8080/ical/<token>.ics"}
```

Paste that URL into iOS Calendar → Add Account → Other → Add Subscribed
Calendar, or Google Calendar → Other Calendars → From URL. Your calendar
app will pull the feed roughly hourly (the `X-PUBLISHED-TTL: PT1H` hint).

### Revoke

```bash
curl -s -X DELETE -H "Authorization: Bearer $ACCESS" \
  http://localhost:8080/me/ical-token  # → 204
```

The old URL stops working immediately. Generate a new one via POST.

## Terraform bootstrap + CI/CD pipelines quickstart

Bootstrap creates the AWS-side scaffolding: state backend (S3 + DynamoDB),
GitHub CodeStar connection, ECR repo, SNS approval topic, IAM roles, and
two CodePipelines (infra + app). Run this **once** from a developer laptop
with AWS admin credentials.

```bash
cd terraform/bootstrap

# Configure your inputs
cp terraform.tfvars.example terraform.tfvars
# Open terraform.tfvars and set approval_email at minimum.

# Apply
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

After apply, the outputs print three required follow-ups:

1. **Authorize the GitHub CodeStar Connection.** Terraform creates it in
   `PENDING` state. Go to AWS Console → Developer Tools → Settings →
   Connections → `hwh-github` → Update pending connection → authorize the
   `AWS Connector for GitHub` app on the `wmyers/heres-whats-happening` repo.
   Until you do this, the pipelines fail at their Source stage.

2. **Confirm the SNS email subscription.** AWS emails the address you set
   in `approval_email`. Click the link to confirm. Without confirmation,
   manual-approval notifications won't arrive.

3. **Store the local Terraform state file safely.**
   `terraform/bootstrap/terraform.tfstate` is gitignored. Copy it to a
   password manager / encrypted volume — without it you can't update
   bootstrap resources later.

The pipelines exist and will run on every push to `master`, but they're
no-op until:
- `terraform/prod/` is populated (see the Terraform prod infrastructure
  quickstart) — the infra pipeline will then start producing meaningful
  plans for the manual-approval gate.
- An ECS service exists (same quickstart) — the app pipeline's Deploy stage will
  then actually update the running task definition. Until then, it just
  pushes the image to ECR.

## React + Vite frontend quickstart

The SPA lives in `web/`. In dev it runs on Vite (port 5173) and proxies API
calls to the Go backend on port 8080.

```bash
# One-time setup
cd web
pnpm install

# Daily dev (alongside `make run` for the API)
pnpm dev
# Open http://localhost:5173
```

### Tests

```bash
cd web
pnpm test          # one-shot
pnpm test:watch    # watch mode
```

### Production build + deploy

The deploy script builds, syncs to S3, and invalidates CloudFront. Bucket name +
distribution ID come from the prod Terraform outputs (Terraform prod
infrastructure quickstart).

```bash
# Configure once (gitignored)
cat > web/.env.deploy <<EOF
S3_BUCKET=heres-whats-happening-frontend
CLOUDFRONT_DISTRIBUTION_ID=E2XXXXXXXXX
VITE_API_BASE_URL=https://api.example.com
EOF

# Deploy
cd web && pnpm deploy
```

### Production CORS

The Go API needs `CORS_ALLOWED_ORIGINS=https://example.com` set so the SPA
can call cross-origin from CloudFront → ALB. In dev this is unnecessary
(Vite's proxy makes everything same-origin).

## Terraform prod infrastructure quickstart

This is the big one — the actual production runtime. Requires the Terraform
bootstrap + CI/CD pipelines quickstart to have been completed, and the
prerequisites listed in `docs/superpowers/plans/2026-05-26-plan-08-terraform-prod.md`.

### Prereqs (one-time)

1. The Terraform bootstrap is applied (you already did this — the bootstrap
   outputs printed the state bucket name).
2. Register a domain. Create a Route53 public hosted zone for it. Update your
   registrar's nameservers to the four NS records in the zone. Wait for DNS
   propagation (`dig +short NS your-domain.com` returns the AWS nameservers).
3. Replace `REPLACE_WITH_ACCOUNT_ID` in `terraform/prod/backend.tf` with your
   AWS account ID (visible in any IAM resource ARN from the bootstrap outputs, or via
   `aws sts get-caller-identity --query Account --output text`).

### Apply

```bash
cd terraform/prod
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars and set domain_name to your real domain.
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

First apply takes ~15 minutes (RDS, CloudFront, ACM cert validation).

### Post-apply checklist

The outputs print a `post_apply_steps` heredoc — follow it:

1. **Seed Secrets Manager values** — Terraform creates the secret shells with
   placeholder values; you write the real secrets via `aws secretsmanager
   put-secret-value` for `jwt-signing-key`, `spotify-client-id`, etc.
2. **Push a bootstrap image to ECR** — needed before the first ECS service
   deploy. The output prints the exact docker commands.
3. **Trigger the app pipeline** — push a commit to master, or manually start
   the `hwh-app-pipeline` in the AWS console. This builds the real Go image
   and rolls the api service.
4. **Run database migrations** — connect to RDS using the master credentials
   from Secrets Manager, run all migrations under `sql/migrations/`. A future
   plan should automate this; for v1 it's a one-time setup.
5. **Deploy the frontend** — fill in `web/.env.deploy` with the bucket name +
   distribution ID from the outputs, then `cd web && pnpm deploy`.

After all five: hit `https://api.your-domain.com/healthz` (should return
`{"status":"ok"}`) and load `https://your-domain.com` in a browser.
