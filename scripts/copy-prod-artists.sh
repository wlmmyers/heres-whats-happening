#!/usr/bin/env bash
# Copy prod's artist enrichment into the local dev DB.
#
#   ./copy-prod-artists.sh            # dump + load
#   ./copy-prod-artists.sh --dump-only
#
# Opens its own SSM port-forward on 5433 (NOT the Makefile's 5432, which the
# local docker Postgres already owns) and closes it on exit.
set -euo pipefail
cd "$(dirname "$0")"

AWS_PROFILE=${AWS_PROFILE:-servant}
PROD_REGION=${PROD_REGION:-us-east-1}

# PROD_DB_HOST and PROD_SECRET_ARN name prod infrastructure, so they stay out of
# this file and live in .env (gitignored) — the same place the Makefile's bastion
# targets read them from. Read here explicitly because this script runs directly
# rather than through make, so it never inherits make's `include .env`.
#
# Only these two keys are pulled in; .env is NOT sourced wholesale. It also
# carries AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY=local for ElasticMQ, and
# exporting those static dummies would take precedence over AWS_PROFILE and fail
# every aws call below. Anything already exported by the caller wins.
if [ -f ../.env ]; then
    # Spelled out as nested ifs, not `[ ... ] && ...` one-liners: under `set -e` a
    # trailing AND-list whose test fails takes the whole script down with it, and
    # a failing test is the normal path here.
    for key in PROD_DB_HOST PROD_SECRET_ARN; do
        if [ -z "${!key:-}" ]; then
            val=$(sed -n "s/^[[:space:]]*${key}=//p" ../.env | tail -1)
            if [ -n "$val" ]; then
                export "$key=$val"
            fi
        fi
    done
fi
: "${PROD_DB_HOST:?not set — add it to .env (see the bastion section of the Makefile)}"
: "${PROD_SECRET_ARN:?not set — add it to .env (see the bastion section of the Makefile)}"

# Unlike the two above, the bastion's Name tag is not sensitive and is already
# the default in the Makefile; keep the two in sync.
BASTION_NAME=${BASTION_NAME:-hwh-bastion}
TUNNEL_PORT=${TUNNEL_PORT:-5433}
export AWS_PROFILE

LOCAL_CONN="host=127.0.0.1 port=${LOCAL_DB_PORT:-5432} dbname=appdb user=app sslmode=disable"
PROD_CONN="host=127.0.0.1 port=${TUNNEL_PORT} dbname=appdb user=app sslmode=require"

# --- guard: the load target must be the docker container, never prod ----------
# Compares the cluster's system_identifier against the one read through
# `docker exec`, which cannot reach anything but the local container. A
# mistyped port that lands on the tunnel aborts here instead of writing to prod.
assert_local() {
    local via_docker via_tcp
    via_docker=$(docker exec hwh_postgres psql -U app -d appdb -tAc \
        'SELECT system_identifier FROM pg_control_system()')
    via_tcp=$(PGPASSWORD=${LOCAL_DB_PASSWORD:-app} psql "$LOCAL_CONN" -tAc \
        'SELECT system_identifier FROM pg_control_system()')
    if [ -z "$via_docker" ] || [ "$via_docker" != "$via_tcp" ]; then
        echo "REFUSING TO WRITE: $LOCAL_CONN is not the local hwh_postgres container." >&2
        exit 1
    fi
    echo "load target verified local (system_identifier $via_tcp)"
}

# --- tunnel ------------------------------------------------------------------
INSTANCE=$(aws ec2 describe-instances --region "$PROD_REGION" \
    --filters "Name=tag:Name,Values=$BASTION_NAME" "Name=instance-state-name,Values=running" \
    --query 'Reservations[].Instances[].InstanceId' --output text)
[ -n "$INSTANCE" ] || { echo "no running bastion tagged $BASTION_NAME (make bastion-start)" >&2; exit 1; }

echo "opening tunnel to $PROD_DB_HOST:5432 on localhost:$TUNNEL_PORT via $INSTANCE ..."
# `sleep` holds stdin open: session-manager-plugin exits as soon as stdin closes.
# stdin is /dev/null deliberately. A FIFO or any other never-EOF stdin stops
# session-manager-plugin from ever opening the port; with /dev/null it opens in
# ~15s and stays up as long as the process lives.
aws ssm start-session --region "$PROD_REGION" --target "$INSTANCE" \
    --document-name AWS-StartPortForwardingSessionToRemoteHost \
    --parameters "{\"host\":[\"$PROD_DB_HOST\"],\"portNumber\":[\"5432\"],\"localPortNumber\":[\"$TUNNEL_PORT\"]}" \
    </dev/null >tunnel.log 2>&1 &
TUNNEL_PID=$!
cleanup() {
    kill "$TUNNEL_PID" 2>/dev/null || true
    wait "$TUNNEL_PID" 2>/dev/null || true
}
trap cleanup EXIT

for _ in $(seq 1 45); do nc -z 127.0.0.1 "$TUNNEL_PORT" 2>/dev/null && break; sleep 1; done
nc -z 127.0.0.1 "$TUNNEL_PORT" || { echo "tunnel never came up; see tunnel.log" >&2; exit 1; }

# Fetched fresh every run: the RDS master password rotates on a 7-day schedule.
PGPASSWORD=$(aws secretsmanager get-secret-value --region "$PROD_REGION" \
    --secret-id "$PROD_SECRET_ARN" --query SecretString --output text | jq -r '.password')
export PGPASSWORD

# --- stage 1: dump -----------------------------------------------------------
echo
echo "== prod =="
psql "$PROD_CONN" -v ON_ERROR_STOP=1 -c \
    "SELECT (SELECT count(*) FROM artists) artists,
            (SELECT count(*) FROM artist_images) images,
            (SELECT count(*) FROM artist_bios) bios,
            (SELECT count(*) FROM artist_tour_snapshots) tours,
            (SELECT count(*) FROM events WHERE headline_artist_id IS NOT NULL) linked;"

psql "$PROD_CONN" -v ON_ERROR_STOP=1 -f dump-prod-artists.sql
wc -l ./*.tsv

unset PGPASSWORD
cleanup
trap - EXIT

if [ "${1:-}" = "--dump-only" ]; then
    echo "dump only — nothing loaded."
    exit 0
fi

# --- stage 2: load -----------------------------------------------------------
echo
assert_local
export PGPASSWORD=${LOCAL_DB_PASSWORD:-app}
psql "$LOCAL_CONN" -v ON_ERROR_STOP=1 -f load-local-artists.sql

echo
echo "== local =="
psql "$LOCAL_CONN" -c \
    "SELECT (SELECT count(*) FROM artists) artists,
            (SELECT count(*) FROM artist_images) images,
            (SELECT count(*) FROM artist_bios) bios,
            (SELECT count(*) FROM artist_tour_snapshots) tours,
            (SELECT count(*) FROM events) events,
            (SELECT count(*) FROM events WHERE headline_artist_id IS NOT NULL) linked;"
