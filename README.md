# Hako

Hako (箱, "box") is a small, self-hosted dashboard for a personal Mac fleet.
One Go binary provides the macOS worker, server, and embedded web interface.
CPU, memory, storage, and network observations update every two seconds without
refreshing the page. Selecting a machine opens a compact detail drawer.

The server uses SQLite and runs on macOS or Linux. Workers run on macOS.
Each device has a stable UUID and a separately editable display name. Joining
requires an expiring, single-device enrollment key. An administrator can rename
or remove machines and revoke unused enrollment keys from the dashboard.

## Start locally

Use Go 1.27.3 (see `.go-version`) and Xcode Command Line Tools on macOS:

```sh
make build
./scripts/run-local.sh
```

Open <http://127.0.0.1:8787>. Sign in with the administrator key stored in
`.local/admin.key`. The script starts the server and a worker, preserves their
identities across restarts, and stops both on Control-C. Runtime data stays in
the ignored `.local/` directory. No system service is installed.

The worker collects ordinary-user information by default. Use
`./scripts/run-local.sh --root-agent` for privileged read-only observations.
Hako does not run remote commands, install system updates, or modify Mac settings.

For a standalone report, use `./bin/hako inspect`. Reports contain device
identifiers and network addresses; keep saved reports private.

## Install and operate

- [Deployment](docs/deployment.md): Linux or macOS server, HTTPS, worker services,
  enrollment, removal, upgrades, and backups.
- [Security and API](docs/security.md): credential scopes, UUIDs, expiry,
  revocation, and authenticated endpoints.
- [Collection semantics](docs/data-collection.md): fields, cadence, resource
  calculations, and unavailable observations.
- [Release process](docs/releases.md): local artifacts, Apple signing, future
  GitHub draft releases, and publication checks.

There is no hosted service or demo dataset. Hako stores the latest observation
for each machine, not a time-series history. LLDP and session-app collection are
not implemented. One server process owns each SQLite database.

## Development

```sh
make test
make vet
make audit
make build
```

The frontend uses browser-native JavaScript and CSS without a package manager.
SQLite uses the pure-Go `modernc.org/sqlite` driver. macOS CPU sampling uses cgo
and Apple's SDK; builds without cgo retain a command-based fallback. Release
archives include dependency license notices.

Use `SIGN_IDENTITY='your local signing identity' make sign` for a local Apple
signature. This does not notarize or upload the binary. The signing identifier
is `moe.uwucocoa.hako`; the worker service is `moe.uwucocoa.hako.worker`.

MIT licensed. See [LICENSE](LICENSE).
