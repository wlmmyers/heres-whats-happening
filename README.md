# event-calendar

A custom event calendar based on your interests.

See [docs/superpowers/specs/2026-05-19-event-calendar-design.md](docs/superpowers/specs/2026-05-19-event-calendar-design.md) for the v1 design.

## Local dev quickstart (Plan 1 — backend foundation)

Prerequisites: Go 1.24+, Docker, `sqlc` (`go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0`).

```bash
cp .env.example .env

# Start Postgres + pgvector (creates appdb and appdb_test)
make db-up

# Apply migrations to both databases
set -a; source .env.example; set +a
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
