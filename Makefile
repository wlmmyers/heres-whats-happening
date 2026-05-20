.PHONY: db-up db-down db-reset migrate migrate-test test run

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

db-reset:
	docker compose down -v
	docker compose up -d postgres

migrate:
	go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
	  -path sql/migrations \
	  -database "$$DATABASE_URL" up

migrate-test:
	go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
	  -path sql/migrations \
	  -database "$$TEST_DATABASE_URL" up

test:
	go test ./... -count=1

run:
	go run ./cmd/app serve
