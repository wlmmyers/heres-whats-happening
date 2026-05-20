.PHONY: db-up db-down db-reset migrate migrate-test test run queue-up queue-down queue-reset scrape

ifneq (,$(wildcard .env))
    include .env
    export
endif

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
	go test -p 1 ./... -count=1

run:
	go run ./cmd/app serve

queue-up:
	docker compose up -d elasticmq

queue-down:
	docker compose stop elasticmq

queue-reset:
	docker compose down elasticmq -v
	docker compose up -d elasticmq

scrape:
	go run ./cmd/app scrape events --source=ticketmaster
