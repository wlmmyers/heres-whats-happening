# Here's What's Happening

A custom event calendar based on your interests.

See [docs/superpowers/specs/2026-05-19-event-calendar-design.md](docs/superpowers/specs/2026-05-19-event-calendar-design.md) for the v1 design.

## Local dev quickstart (Plan 1 — backend foundation)

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

## Plan 2 quickstart — event ingest

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

## Plan 3 quickstart — Spotify integration

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

1. Sign up + log in via the auth flow (Plan 1 quickstart).
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

## Plan 4 quickstart — match-job

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
