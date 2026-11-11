#!/bin/sh
set -eu
umask 077
cd "$(dirname "$0")/.."
project_dir=$(pwd)
postgres_dir="$project_dir/.local/postgres"
if [ -z "${PG_BINDIR-}" ]; then
  if command -v pg_ctl >/dev/null 2>&1; then PG_BINDIR=$(dirname "$(command -v pg_ctl)")
  elif command -v brew >/dev/null 2>&1; then PG_BINDIR="$(brew --prefix postgresql@18)/bin"
  else printf 'Install PostgreSQL or set PG_BINDIR to its bin directory.\n' >&2; exit 1
  fi
fi
case "${1-start}" in
  stop) exec "$PG_BINDIR/pg_ctl" -D "$postgres_dir/data" -m fast -w stop ;;
  start) ;;
  *) printf 'Usage: %s [start|stop]\n' "$0" >&2; exit 1 ;;
esac
mkdir -p "$postgres_dir/socket"
if [ ! -f "$postgres_dir/data/PG_VERSION" ]; then
  "$PG_BINDIR/initdb" -D "$postgres_dir/data" --username=hako_admin --auth-local=trust --auth-host=reject --encoding=UTF8 --no-locale > "$postgres_dir/init.log"
fi
if ! "$PG_BINDIR/pg_ctl" -D "$postgres_dir/data" status >/dev/null 2>&1; then
  "$PG_BINDIR/pg_ctl" -D "$postgres_dir/data" -l "$postgres_dir/postgres.log" -o "-k '$postgres_dir/socket' -c listen_addresses=''" -w start >/dev/null
fi
export PGHOST="$postgres_dir/socket" PGUSER=hako_admin PGDATABASE=postgres
if [ "$("$PG_BINDIR/psql" -Atc "SELECT 1 FROM pg_roles WHERE rolname='hako'")" != 1 ]; then
  "$PG_BINDIR/psql" -v ON_ERROR_STOP=1 -c 'CREATE ROLE hako LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE' >/dev/null
fi
if [ "$("$PG_BINDIR/psql" -Atc "SELECT 1 FROM pg_database WHERE datname='hako'")" != 1 ]; then
  "$PG_BINDIR/createdb" --owner=hako hako
fi
if [ ! -f "$project_dir/.local/database.url" ]; then
  python3 - "$postgres_dir/socket" > "$project_dir/.local/database.url" <<'PY'
import sys, urllib.parse
print('postgres://hako@/hako?host='+urllib.parse.quote(sys.argv[1],safe='')+'&sslmode=disable')
PY
fi
printf 'Local PostgreSQL is running on a private Unix socket. Connection file: .local/database.url\n'
