#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
# Own the entire loopback cluster; no configured or production database is read.
if [[ $# == 0 ]]; then set -- make check; fi
owned_pg_bin=
if command -v initdb >/dev/null && [[ $(initdb --version) == *"PostgreSQL) 18."* ]]; then
 owned_pg_bin=$(dirname "$(command -v initdb)")
else
 for candidate in /usr/lib/postgresql/*/bin /nix/store/*-postgresql-*/bin; do
  if [[ -x "$candidate/initdb" ]] && [[ $("$candidate/initdb" --version) == *"PostgreSQL) 18."* ]]; then owned_pg_bin=$candidate; break; fi
 done
fi
[[ -n "$owned_pg_bin" ]] || { echo 'PostgreSQL 18 native tools are required' >&2; exit 1; }
export PATH="$owned_pg_bin:$PATH"
owned_root=$(mktemp -d /tmp/moneybag-access-pg.XXXXXXXX)
cleanup(){ pg_ctl -D "$owned_root/data" -m immediate stop >/dev/null 2>&1 || true; rm -rf "$owned_root"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
owned_port=$(python3 - <<'PY'
import socket
with socket.socket() as sock:
 sock.bind(('127.0.0.1',0));print(sock.getsockname()[1])
PY
)
initdb -D "$owned_root/data" --auth-local=trust --auth-host=trust --username=moneybag_test >/dev/null
pg_ctl -D "$owned_root/data" -l "$owned_root/postgres.log" -o "-h 127.0.0.1 -p $owned_port -k $owned_root" start >/dev/null
export MONEYBAG_TEST_DATABASE_URL="postgres://moneybag_test@127.0.0.1:$owned_port/postgres?sslmode=disable"
"$@"
