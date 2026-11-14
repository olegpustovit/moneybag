#!/usr/bin/env bash
# Run off the database VM, so a lost VM does not take its backups with it.
set -euo pipefail
umask 077
target="$HOME/.local/state/moneybag-pg/nightly"
mkdir -p "$target"
archive="$target/moneybag-$(date -u +%Y%m%dT%H%M%SZ).dump"
partial=$(mktemp "$target/.backup.XXXXXXXX")
trap 'rm -f "$partial"' EXIT
ssh -o BatchMode=yes -o ConnectTimeout=15 oracle-vm bash -s > "$partial" <<'REMOTE'
set -eu
. /home/snowbear/ci/moneybag/pg-migration.env
export MONEYBAG_DATABASE_URL
python3 - <<'PY'
import os
from urllib.parse import urlsplit, unquote
url = urlsplit(os.environ.pop('MONEYBAG_DATABASE_URL'))
os.environ.update(PGHOST=url.hostname, PGPORT=str(url.port or 5432),
                  PGUSER=unquote(url.username), PGPASSWORD=unquote(url.password),
                  PGDATABASE=unquote(url.path.lstrip('/')), PGCONNECT_TIMEOUT='15')
os.execvp('pg_dump', ['pg_dump', '--format=custom'])
PY
REMOTE
[[ "$(head -c 5 "$partial")" == PGDMP ]]
mv "$partial" "$archive"
printf 'Backup saved: %s\n' "$archive"
