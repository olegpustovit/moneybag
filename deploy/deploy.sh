#!/usr/bin/env bash
set -Eeuo pipefail
project_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
deploy_host=${MONEYBAG_DEPLOY_HOST:-oracle-vm}
target=/home/snowbear/ci/moneybag

remote() {
 ssh -o BatchMode=yes -o ConnectTimeout=5 "$deploy_host" bash -s -- "$@"
}
verify_local() {
 (cd "$project_root/e2e" && npm ci --ignore-scripts)
 make -C "$project_root" check
 (cd "$project_root/backend" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$release" -o bin/moneybag ./cmd/api)
}
capture_previous() {
 previous=$(remote "$target" <<'REMOTE'
set -euo pipefail
cd "$1"
test -f moneybag.env
previous=$(readlink current)
[[ "$previous" =~ ^releases/[a-zA-Z0-9._-]+$ ]]
test -x "$previous/moneybag"
printf '%s' "$previous"
REMOTE
 )
 [[ "$previous" =~ ^releases/[a-zA-Z0-9._-]+$ ]] || { echo 'A known previous release is required for rollback.' >&2; return 1; }
}
upload_release() {
 local checksum
 checksum=$(sha256sum "$project_root/backend/bin/moneybag" | cut -d ' ' -f 1)
 remote "$target" "$release" <<'REMOTE'
set -euo pipefail
mkdir -p "$1/releases/$2"
REMOTE
 scp "$project_root/backend/bin/moneybag" "$deploy_host:$target/releases/$release/moneybag"
 scp "$project_root/deploy/run" "$project_root/deploy/smdctl.yml" "$deploy_host:$target/"
 remote "$target" "$release" "$checksum" <<'REMOTE'
set -euo pipefail
cd "$1"
printf '%s  %s\n' "$3" "releases/$2/moneybag" | sha256sum --check --status
chmod 755 run "releases/$2/moneybag"
REMOTE
}
activate() {
 remote "$target" "$release" "$previous" <<'REMOTE'
set -euo pipefail
cd "$1"
[[ $(readlink current) == "$3" ]]
ln -s "releases/$2" next
mv -Tf next current
# Reuse the installed unit while preserving its operator-owned environment.
if [[ -f "$HOME/.config/systemd/user/smdctl-moneybag.service" ]]; then smdctl restart moneybag; else smdctl run -f smdctl.yml; fi
for attempt in {1..15}; do
 if curl --connect-timeout 1 --max-time 2 --fail --silent http://127.0.0.1:6459/healthz | python3 -c 'import json,sys; assert json.load(sys.stdin).get("version")==sys.argv[1]' "$2" 2>/dev/null; then exit 0; fi
 sleep 1
done
echo 'Deployment health/SHA check failed.' >&2
exit 1
REMOTE
}
rollback() {
 remote "$target" "$release" "$previous" <<'REMOTE'
set -euo pipefail
cd "$1"
current=$(readlink current)
[[ "$current" != "$3" ]] || exit 0
[[ "$current" == "releases/$2" ]] || { echo 'Current release changed concurrently; preserving it.' >&2; exit 1; }
ln -s "$3" next
mv -Tf next current
smdctl restart moneybag
for attempt in {1..15}; do
 if curl --connect-timeout 1 --max-time 2 --fail --silent http://127.0.0.1:6459/healthz | python3 -c 'import json,sys; assert json.load(sys.stdin).get("version")==sys.argv[1]' "${3#releases/}" 2>/dev/null; then exit 0; fi
 sleep 1
done
echo 'Previous release health/SHA check failed.' >&2
exit 1
REMOTE
}
activation_failed() {
 local status=$?
 trap - ERR
 rollback || echo 'Rollback failed; manual recovery is required.' >&2
 echo 'Deployment activation or authentication smoke failed.' >&2
 exit "$status"
}
main() {
 case "${1:-}" in
  --dry-run)
   printf '%s\n' 'Require clean dev and private persistent eval credentials; run local checks/build; capture previous release; upload/checksum; activate/check SHA; run real browser smoke; roll back on health or smoke failure.'
   return ;;
  '') ;;
  *) echo 'usage: deploy/deploy.sh [--dry-run]' >&2; return 2 ;;
 esac
 cd "$project_root"
 [[ $(git symbolic-ref --quiet --short HEAD) == dev ]] || { echo 'Deploy from dev.' >&2; return 1; }
 [[ -z $(git status --porcelain) ]] || { echo 'Deploy from a clean worktree.' >&2; return 1; }
 node e2e/smoke-auth.mjs --check-config
 release="$(git rev-parse --short=12 HEAD)-$(date -u +%Y%m%d%H%M%S)"
 verify_local
 capture_previous
 upload_release
 trap activation_failed ERR
 activate
 MONEYBAG_SMOKE_VERSION="$release" node e2e/production-smoke.mjs
 trap - ERR
 printf 'Deployment and persistent eval authentication healthy: %s\n' "$release"
}
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
