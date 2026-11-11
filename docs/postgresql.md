# PostgreSQL

Hako requires PostgreSQL 18. It uses `pgx`, native UUID device keys, TIMESTAMPTZ
metadata, and JSONB latest snapshots. Workers talk only to the Hako HTTP API;
they do not receive database credentials or connect to PostgreSQL.

## Create the database

Use an existing PostgreSQL service or a dedicated installation. As a database
administrator, create a dedicated login and database:

```sql
CREATE ROLE hako LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE DATABASE hako OWNER hako;
```

Set its password interactively using `\password hako` in psql. Configure access
rules for the server host. Hako owns tables in its dedicated database, needs
permission to apply schema migrations, and does not need superuser privileges.

Save the connection URL in a private file with mode 0600, for example:

```text
postgres://hako:REPLACE_WITH_PASSWORD@db.example.com:5432/hako?sslmode=verify-full
```

Percent-encode reserved characters in URL credentials. Use verified TLS for
remote connections and provide `sslrootcert` if the server uses a private CA.
A local Unix socket can use peer authentication and `sslmode=disable`. Connection
credentials follow [pgx configuration](https://pkg.go.dev/github.com/jackc/pgx/v5).
Do not put real URLs with passwords into command arguments, Git, or shared logs.

Pass `--database-url-file /private/path/database.url` to `hako serve`, `hako init`,
or `hako import`. Alternatively set `HAKO_DATABASE_URL` through the service's
secret/environment configuration. A file takes precedence over the environment.
The server refuses to start without a PostgreSQL connection. There is no SQLite
fallback and no database password in worker configurations.

## Local development

On macOS, install PostgreSQL 18 from Homebrew and run:

```sh
brew install postgresql@18
./scripts/postgres-local.sh
make build
./scripts/run-local.sh
```

The helper initializes `.local/postgres`, starts PostgreSQL as your normal user,
and writes `.local/database.url`. It listens on a Unix socket inside a private
0700 directory, with no TCP listener. Local trust authentication is limited by
that directory's permissions. This is a development arrangement; production
should use its normal database authentication and service management.

The application connects with a dedicated, non-superuser `hako` role. Stop the
local database separately with `./scripts/postgres-local.sh stop`. The helper
preserves existing data and does not register a login or system service. Set
`PG_BINDIR` if PostgreSQL tools are not discoverable on PATH or through Homebrew.

`make test` creates and removes a separate, disposable PostgreSQL cluster. It
never uses `.local/database.url`. To test against another PostgreSQL instance,
set `HAKO_TEST_DATABASE_URL` to a test-only administrative connection whose role
can create and drop databases. Every server test uses a unique temporary database.
Do not supply a production database administrator connection for this purpose.

## Schema and receiver ownership

Versioned SQL migrations are embedded in the binary. Startup applies missing
migrations in a transaction and rejects schemas newer than the running binary.
Registration claims and device creation commit atomically. Revocation and
snapshot deletion also commit together. Foreign keys protect device references.

This version still runs one Hako receiver for each database. It holds a PostgreSQL
session advisory lock on the same connection used for reads and writes. A second
receiver is rejected, including one using a different local data directory. A
lost database connection cannot silently reconnect while retaining stale cached
credentials; the receiver exits and its service manager restarts it. `/healthz`
returns 503 when its database connection is unavailable. Active-active receivers
and cross-process event fan-out are separate future work.

Database operations have connection, query, and lock timeouts. Hako requests
synchronous commits; keep PostgreSQL's normal durability settings enabled.
See [PostgreSQL advisory locks](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS).

## Move existing SQLite state

Stop the old receiver and workers for a consistent cutover. Keep a private backup
of the original SQLite data directory, the old binary, and `admin.key`. Export
with Python 3, which is needed only for this prototype migration:

```sh
python3 scripts/export-sqlite.py \
  --database ./old-server-data/hako.db --output-dir ./private-export
```

The export reads SQLite without modifying it and refuses an existing output
directory. It contains credential digests and sensitive device information;
keep it private. Prepare an empty PostgreSQL database and copy the original
`admin.key` into the new server's data directory with mode 0600, then run:

```sh
./hako import --data-dir ./server-data \
  --database-url-file ./database.url --from-dir ./private-export
./hako serve --data-dir ./server-data \
  --database-url-file ./database.url --listen 127.0.0.1:8787
```

Reusing the original server data directory is also supported. Startup detects
unmigrated SQLite state there and refuses to silently start an empty fleet.
Import requires an empty PostgreSQL application database, checks the original
administrator-key digest, and commits all records together. UUIDs, names, worker
credential digests, registration consumption, revocations, sessions, and latest
snapshots are preserved. Workers keep their existing configurations.

Verify the fleet and resume workers. Remove the temporary export after checking
the migration. Retain the private pre-migration backup until the deployment is
accepted. Do not restart the old receiver after cutover unless intentionally
rolling back; subsequent PostgreSQL changes will not exist in the SQLite copy.

## Back up and restore

Use PostgreSQL backups, not copies of the Hako data directory alone. For a small
installation, `pg_dump --format=custom --file=hako.dump` with a protected psql
service/passfile configuration makes a consistent logical backup while the server
runs. Keep dumps private and back up `admin.key` separately. Include database
roles and infrastructure configuration in your recovery plan. Larger deployments
can use their existing physical backups and point-in-time recovery.

Restore into an empty database with `pg_restore --no-owner --exit-on-error`, using
the dedicated database owner and the same connection configuration. Stop Hako
during restore, preserve its original administrator key, and use a binary that
supports the restored schema. Test restores regularly. An old backup can restore
old sessions or revoked credentials; review them before reopening access.

See the official [pg_dump documentation](https://www.postgresql.org/docs/18/app-pgdump.html)
and [backup and restore guide](https://www.postgresql.org/docs/18/backup.html).
