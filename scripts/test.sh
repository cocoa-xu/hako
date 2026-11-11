#!/bin/sh
set -eu
if [ -n "${HAKO_TEST_DATABASE_URL-}" ]; then exec "${GO-go}" test -race ./...; fi
if [ -z "${PG_BINDIR-}" ]; then
  if command -v pg_ctl >/dev/null 2>&1; then PG_BINDIR=$(dirname "$(command -v pg_ctl)")
  elif command -v brew >/dev/null 2>&1; then PG_BINDIR="$(brew --prefix postgresql@18)/bin"
  else printf 'Install PostgreSQL or set HAKO_TEST_DATABASE_URL.\n' >&2; exit 1
  fi
fi
scratch=$(mktemp -d /tmp/hako-pg-test.XXXXXX)
cleanup() {
  "$PG_BINDIR/pg_ctl" -D "$scratch/data" -m immediate -w stop >/dev/null 2>&1 || true
  rm -rf "$scratch"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
"$PG_BINDIR/initdb" -D "$scratch/data" --username=hako_test --auth-local=trust --auth-host=reject --encoding=UTF8 --no-locale > "$scratch/init.log"
"$PG_BINDIR/pg_ctl" -D "$scratch/data" -l "$scratch/postgres.log" -o "-k $scratch -c listen_addresses=''" -w start >/dev/null
export HAKO_TEST_DATABASE_URL="postgres://hako_test@/postgres?host=$scratch&sslmode=disable"
"${GO-go}" test -race ./...
