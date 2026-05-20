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
