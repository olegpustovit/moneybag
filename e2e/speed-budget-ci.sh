#!/usr/bin/env bash
# One command for the SPA speed budget: build, start the binary on a throwaway database, seed six months of synthetic data, run the budget.
# MONEYBAG_E2E_MONGO_URI points at a Mongo to use (CI's service); without it a Docker mongo:8 starts on 27096 and is removed on exit.
# SKIP_BUILD=1 reuses backend/bin/moneybag-local. CHROMIUM_BIN names the browser the budget script drives.
set -euo pipefail
cd "$(dirname "$0")/.."
PORT="${SPEED_PORT:-16603}" TOKEN=synthetic-preview-token LOG="${TMPDIR:-/tmp}/speed-budget-server.log"
server='' container=''
cleanup() {
  [ -n "$server" ] && kill "$server" 2>/dev/null || true
  [ -n "$container" ] && docker rm -f "$container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if [ -z "${MONEYBAG_E2E_MONGO_URI:-}" ]; then
  container=moneybag-speed-mongo
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker run -d --name "$container" --tmpfs /data/db:size=1g -p 127.0.0.1:27096:27017 mongo:8 --wiredTigerCacheSizeGB 0.5 >/dev/null
  until docker exec "$container" mongosh --quiet --eval 'db.runCommand({ping:1}).ok' >/dev/null 2>&1; do sleep 0.3; done
  MONEYBAG_E2E_MONGO_URI=mongodb://127.0.0.1:27096
fi

[ -n "${SKIP_BUILD:-}" ] || timeout 240 make build
(cd e2e && PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 timeout 60 npm ci --no-audit --no-fund --silent)

MONEYBAG_LISTEN="127.0.0.1:$PORT" MONEYBAG_PUBLIC_URL="http://127.0.0.1:$PORT" MONEYBAG_MONGO_URI="$MONEYBAG_E2E_MONGO_URI" MONEYBAG_MONGO_DB=moneybag_speed_ci \
  MONEYBAG_SETUP_TOKEN="$TOKEN" MONEYBAG_SECRET_KEY="$(head -c 32 /dev/urandom | base64)" backend/bin/moneybag-local >"$LOG" 2>&1 &
server=$!
for _ in $(seq 100); do curl -fs "http://127.0.0.1:$PORT/login" >/dev/null && break; sleep 0.2; done
curl -fs "http://127.0.0.1:$PORT/login" >/dev/null || { cat "$LOG"; exit 1; }

cd e2e
export MONEYBAG_SETUP_TOKEN="$TOKEN"
SEED_MONTHS="${SEED_MONTHS:-6}" timeout 150 node seed-telemetry.mjs "http://127.0.0.1:$PORT" | tail -3
timeout 60 node spa-speed-budget.mjs "http://127.0.0.1:$PORT"
