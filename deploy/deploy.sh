#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
: "${MONEYBAG_DEPLOY_HOST:=oracle-vm}"
release="$(git rev-parse --short=12 HEAD)-$(date -u +%Y%m%d%H%M%S)"
target=/home/snowbear/ci/moneybag
(cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$release" -o bin/moneybag ./cmd/api)
ssh "$MONEYBAG_DEPLOY_HOST" mkdir -p "$target/releases/$release"
scp backend/bin/moneybag "$MONEYBAG_DEPLOY_HOST:$target/releases/$release/moneybag"
scp deploy/run deploy/smdctl.yml "$MONEYBAG_DEPLOY_HOST:$target/"
ssh "$MONEYBAG_DEPLOY_HOST" bash -s -- "$target" "$release" <<'REMOTE'
set -euo pipefail
cd "$1"
test -f moneybag.env
chmod 600 moneybag.env
chmod 755 run "releases/$2/moneybag"
previous=$(readlink current || true)
ln -s "releases/$2" next
mv -Tf next current
smdctl run -f smdctl.yml
healthy=false
for attempt in {1..15}; do
 if curl --fail --silent http://127.0.0.1:6459/healthz | python3 -c 'import json,sys; sys.exit(json.load(sys.stdin).get("version") != sys.argv[1])' "$2"; then healthy=true; break; fi
 sleep 1
done
if ! "$healthy"; then
 if [[ -n "$previous" ]]; then ln -s "$previous" next; mv -Tf next current; smdctl restart moneybag; fi
 echo 'Deployment failed health check; restored previous release when available.' >&2
 exit 1
fi
printf '%s\n' "Deployment healthy: $2"
REMOTE
