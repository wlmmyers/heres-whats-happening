#!/usr/bin/env bash
set -euo pipefail

# Plan 6 frontend deploy: build the SPA, sync to S3, invalidate CloudFront.
#
# Requires the following env vars (set them in your shell or via web/.env.deploy
# which is gitignored). Plan 8 creates the bucket + distribution and prints
# their identifiers as terraform outputs.
#   S3_BUCKET=<bucket name, e.g. heres-whats-happening-frontend>
#   CLOUDFRONT_DISTRIBUTION_ID=<E2XXXXXXXXX>
#   VITE_API_BASE_URL=https://api.example.com   (also baked into the build)

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# Source .env.deploy if it exists (gitignored).
if [ -f ".env.deploy" ]; then
  # shellcheck disable=SC1091
  set -a; source .env.deploy; set +a
fi

: "${S3_BUCKET:?S3_BUCKET must be set (e.g. via web/.env.deploy)}"
: "${CLOUDFRONT_DISTRIBUTION_ID:?CLOUDFRONT_DISTRIBUTION_ID must be set}"
: "${VITE_API_BASE_URL:?VITE_API_BASE_URL must be set (e.g. https://api.example.com)}"

echo "==> Building (VITE_API_BASE_URL=$VITE_API_BASE_URL)"
pnpm run build

echo "==> Syncing dist/ → s3://$S3_BUCKET/"
aws s3 sync dist/ "s3://$S3_BUCKET/" --delete

echo "==> Invalidating CloudFront distribution $CLOUDFRONT_DISTRIBUTION_ID"
aws cloudfront create-invalidation \
  --distribution-id "$CLOUDFRONT_DISTRIBUTION_ID" \
  --paths "/*" \
  >/dev/null

echo "✓ Deploy complete"
