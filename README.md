# Hako

Hako (箱, "box") is a small, self-hosted dashboard for a personal Mac fleet.
One Go binary provides the macOS worker, server, and embedded web interface.
CPU, memory, storage, and network observations update every two seconds without
refreshing the page. Selecting a machine opens a compact detail drawer.

The server requires PostgreSQL 18 and runs on macOS or Linux. Workers run on macOS.
Each device has a stable UUID and a separately editable display name. Joining
requires an expiring, single-device enrollment key. An administrator can rename
or remove machines and revoke unused enrollment keys from the dashboard.

## Start locally

Use Go 1.27.3 (see `.go-version`), PostgreSQL 18, and Xcode Command Line Tools on macOS:

```sh
brew install postgresql@18
./scripts/postgres-local.sh
make build
./scripts/run-local.sh
```

Open <http://127.0.0.1:8787>. Sign in with the administrator key stored in
`.local/admin.key`. The script starts the server and a worker, preserves their
identities across restarts, and stops both on Control-C. Runtime data stays in
the ignored `.local/` directory. No system service is installed. The PostgreSQL helper runs a local development
database on a private Unix socket. Stop it separately with
`./scripts/postgres-local.sh stop`. For an existing database, supply
`HAKO_DATABASE_URL` or the private `.local/database.url` file instead.

Upgrading from the SQLite prototype requires the [one-time migration](docs/postgresql.md#move-existing-sqlite-state) before starting the new receiver.

The worker collects ordinary-user information by default. Use
`./scripts/run-local.sh --root-agent` for privileged read-only observations.
Hako does not run remote commands, install system updates, or modify Mac settings.

For a standalone report, use `./bin/hako inspect`. Reports contain device
identifiers and network addresses; keep saved reports private.

## Install and operate

- [Deployment](docs/deployment.md): Linux or macOS server, HTTPS, worker services,
  enrollment, removal, upgrades, and backups.
- [Cloudflare Access](docs/cloudflare.md): per-worker Service Tokens, upgrades,
  proxy configuration, and optional external administrator authentication.
- [PostgreSQL](docs/postgresql.md): database setup, migrations, and backup/restore.
- [Security and API](docs/security.md): credential scopes, UUIDs, expiry,
  revocation, and authenticated endpoints.
- [Collection semantics](docs/data-collection.md): fields, cadence, resource
  calculations, and unavailable observations.
- [Release process](docs/releases.md): local artifacts, Apple signing,
  GitHub releases, and publication checks.

There is no hosted service or demo dataset. Hako stores the latest observation
for each machine, not a time-series history. LLDP and session-app collection are
not implemented. One server process owns each PostgreSQL database. PostgreSQL is the only
runtime persistence backend; workers do not connect to it.

## Development

```sh
make test
make vet
make audit
make build
```

The frontend uses browser-native JavaScript and CSS without a package manager.
PostgreSQL uses the `pgx` driver. `make test` starts and removes a disposable
PostgreSQL cluster, or uses `HAKO_TEST_DATABASE_URL` with a role allowed to create
and drop isolated test databases. macOS CPU sampling uses cgo
and Apple's SDK; builds without cgo retain a command-based fallback. Release
archives include dependency license notices.

Use `SIGN_IDENTITY='your local signing identity' make sign` for a local Apple
signature. This does not notarize or upload the binary. The signing identifier
is `moe.uwucocoa.hako`; the worker service is `moe.uwucocoa.hako.worker`.

MIT licensed. See [LICENSE](LICENSE).
