.PHONY: db-up db-down db-reset migrate migrate-test migrate-prod migrate-prod-status test run run-web run-all queue-up queue-down queue-reset scrape tei-up tei-down tei-seed match

ifneq (,$(wildcard .env))
    include .env
    export
endif

# --- Prod migration (one-off ECS task) ---------------------------------------
# RDS is private (no public access, no NAT), so migrations run inside the VPC as
# a one-off Fargate task on the api task def with its command overridden to
# `migrate`. Override any of these on the CLI, e.g. `make migrate-prod AWS_PROFILE=other`.
AWS_PROFILE ?= servant
PROD_REGION ?= us-east-1
ECS_CLUSTER ?= hwh-cluster
ECS_TASKDEF ?= hwh-api
# Tag applied to the migrate task so `migrate-prod-status` can find it again.
MIGRATE_STARTED_BY ?= migrate-cli
# CloudWatch log group + stream prefix for the api container (awslogs: <prefix>/<container>/<task-id>).
LOG_GROUP ?= /aws/ecs/hwh/api
LOG_STREAM_PREFIX ?= api/api

db-up:
	docker compose up -d postgres

db-down:
	docker compose down

db-reset:
	docker compose down -v
	docker compose up -d postgres

# Dev DB: the app binary assembles the DSN from DB_* (internal/dsn) and applies
# the embedded migrations — the same code path as prod.
migrate:
	go run ./cmd/app migrate

# Test DB: same server, separate db. Map TEST_DB_* onto the DB_* the binary reads.
# godotenv.Load() does not override already-set vars, so these win over .env.
migrate-test:
	DB_USER="$$TEST_DB_USER" DB_PASSWORD="$$TEST_DB_PASSWORD" DB_HOST="$$TEST_DB_HOST" \
	DB_PORT="$$TEST_DB_PORT" DB_NAME="$$TEST_DB_NAME" DB_SSLMODE="$$TEST_DB_SSLMODE" \
	go run ./cmd/app migrate

# Apply prod migrations via a one-off ECS task. The app binary embeds the SQL and
# applies it via its `migrate` subcommand; idempotent and tracked in schema_migrations.
migrate-prod:
	@echo "Running prod migrations (profile=$(AWS_PROFILE) region=$(PROD_REGION) cluster=$(ECS_CLUSTER) taskdef=$(ECS_TASKDEF))..."
	@SUBNETS=$$(aws ec2 describe-subnets --profile $(AWS_PROFILE) --region $(PROD_REGION) \
	    --filters "Name=tag:Name,Values=hwh-public-*" \
	    --query 'Subnets[].SubnetId' --output text | tr '\t' ','); \
	SG=$$(aws ec2 describe-security-groups --profile $(AWS_PROFILE) --region $(PROD_REGION) \
	    --filters "Name=group-name,Values=hwh-task-runner" \
	    --query 'SecurityGroups[0].GroupId' --output text); \
	NETCFG="awsvpcConfiguration={subnets=[$$SUBNETS],securityGroups=[$$SG],assignPublicIp=ENABLED}"; \
	aws ecs run-task --profile $(AWS_PROFILE) --region $(PROD_REGION) \
	    --cluster $(ECS_CLUSTER) --launch-type FARGATE --task-definition $(ECS_TASKDEF) \
	    --started-by $(MIGRATE_STARTED_BY) \
	    --network-configuration "$$NETCFG" \
	    --overrides '{"containerOverrides":[{"name":"api","command":["migrate"]}]}'
	@echo "Launched. Check the result with: make migrate-prod-status"

# Report the most recent prod-migration task: its exit code (0 = success) and its
# CloudWatch logs. Finds the task by the started-by tag set in migrate-prod.
migrate-prod-status:
	@STOPPED=$$(aws ecs list-tasks --profile $(AWS_PROFILE) --region $(PROD_REGION) \
	    --cluster $(ECS_CLUSTER) --started-by $(MIGRATE_STARTED_BY) --desired-status STOPPED \
	    --query 'taskArns' --output text); \
	RUNNING=$$(aws ecs list-tasks --profile $(AWS_PROFILE) --region $(PROD_REGION) \
	    --cluster $(ECS_CLUSTER) --started-by $(MIGRATE_STARTED_BY) --desired-status RUNNING \
	    --query 'taskArns' --output text); \
	ARNS=$$(echo "$$STOPPED $$RUNNING" | tr '[:space:]' '\n' | grep '^arn:aws:ecs' | tr '\n' ' '); \
	if [ -z "$$ARNS" ]; then \
	    echo "No migrate task found (started-by=$(MIGRATE_STARTED_BY))."; \
	    echo "It may have aged out of ECS; logs may still be in CloudWatch group $(LOG_GROUP)."; \
	    exit 1; \
	fi; \
	TASK=$$(aws ecs describe-tasks --profile $(AWS_PROFILE) --region $(PROD_REGION) \
	    --cluster $(ECS_CLUSTER) --tasks $$ARNS \
	    --query 'sort_by(tasks,&createdAt)[-1].taskArn' --output text); \
	echo "Most recent migrate task: $$TASK"; \
	aws ecs describe-tasks --profile $(AWS_PROFILE) --region $(PROD_REGION) \
	    --cluster $(ECS_CLUSTER) --tasks "$$TASK" \
	    --query 'tasks[0].{lastStatus:lastStatus,exitCode:containers[0].exitCode,stoppedReason:stoppedReason,createdAt:createdAt,stoppedAt:stoppedAt}' \
	    --output table; \
	TASK_ID=$${TASK##*/}; \
	echo "---- logs ($(LOG_GROUP) : $(LOG_STREAM_PREFIX)/$$TASK_ID) ----"; \
	aws logs tail $(LOG_GROUP) --profile $(AWS_PROFILE) --region $(PROD_REGION) \
	    --log-stream-names $(LOG_STREAM_PREFIX)/$$TASK_ID --since 3h --format short || \
	    echo "(no logs yet — task may still be starting, or logs not flushed)"

test:
	go test -p 1 ./... -count=1

run:
	go run ./cmd/app serve

run-web:
	cd web && pnpm dev

run-all:
	@trap 'kill 0' INT TERM EXIT; \
	$(MAKE) run & \
	$(MAKE) run-web & \
	wait

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
