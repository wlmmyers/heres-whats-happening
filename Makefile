.PHONY: db-up db-down db-reset migrate migrate-test test run queue-up queue-down queue-reset scrape tei-up tei-down tei-seed match

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

tei-seed:
	pip3 install --quiet --break-system-packages huggingface_hub 2>/dev/null || pip3 install --quiet huggingface_hub
	python3 -c "\
from huggingface_hub import snapshot_download; \
import subprocess, os; \
local = snapshot_download('BAAI/bge-small-en-v1.5', local_dir='/tmp/_tei_dl', ignore_patterns=['*.msgpack','*.h5','flax_model*','tf_model*','rust_model*']); \
sha = open(os.path.expanduser('~/.cache/huggingface/hub/models--BAAI--bge-small-en-v1.5/refs/main')).read().strip(); \
dst = f'.tei-cache/models--BAAI--bge-small-en-v1.5/snapshots/{sha}'; \
os.makedirs(f'.tei-cache/models--BAAI--bge-small-en-v1.5/refs', exist_ok=True); \
open(f'.tei-cache/models--BAAI--bge-small-en-v1.5/refs/main','w').write(sha); \
os.makedirs(dst, exist_ok=True); \
subprocess.run(['cp','-r',local+'/.',dst], check=True); \
print('Model seeded to .tei-cache')"

tei-up:
	docker compose up -d tei

tei-down:
	docker compose stop tei

match:
	go run ./cmd/app match
