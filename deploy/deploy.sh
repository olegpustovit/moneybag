#!/usr/bin/env bash
# Deploy moneybag to oracle-vm through Aquifer (see the aquifer-deploy skill).
set -Eeuo pipefail
project_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
deploy_host=${MONEYBAG_DEPLOY_HOST:-oracle-vm}
ssh_options=(-o BatchMode=yes -o ConnectTimeout=5)

verify_local() {
 (cd "$project_root/e2e" && npm ci --ignore-scripts)
 GOFLAGS=-p=4 bash "$project_root/deploy/test-owned-postgres.sh" make -C "$project_root" check
 (cd "$project_root/backend" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$release" -o bin/moneybag ./cmd/api)
}
capture_previous() { :; }
upload_release() {
 remote="/tmp/moneybag-$release"
 scp "${ssh_options[@]}" "$project_root/backend/bin/moneybag" "$deploy_host:$remote"
}
activate() {
 local status=0
 ssh "${ssh_options[@]}" "$deploy_host" aq deploy moneybag "$remote" || status=$?
 ssh "${ssh_options[@]}" "$deploy_host" unlink "$remote" || true
 ((status == 0)) || return "$status"
 ssh "${ssh_options[@]}" "$deploy_host" curl -fsS --max-time 5 http://127.0.0.1:6459/healthz |
  python3 -c 'import json,sys; assert json.load(sys.stdin).get("version")==sys.argv[1]' "$release"
}
rollback() { ssh "${ssh_options[@]}" "$deploy_host" aq rollback moneybag; }
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
   printf '%s\n' 'Require clean dev and private persistent eval credentials; run local checks/build; scp the binary; aq deploy moneybag (health-checked, automatic rollback); check version; run real browser smoke; roll back on health or smoke failure.'
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
