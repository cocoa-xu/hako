# Deployment

## Choose a server

Use a small Linux host with systemd, or a Mac running a user LaunchAgent. The
server runs as an ordinary user and needs no root privileges. Prepare a dedicated
PostgreSQL 18 database and a private connection file before installing Hako;
see [PostgreSQL setup](postgresql.md). Run exactly one receiver per database.
The server installer configures Hako, not the PostgreSQL service.

For remote workers, put an HTTPS reverse proxy in front of the loopback listener.
Set `--public-url` to the exact browser-facing HTTPS origin. Preserve the Host
header and disable response buffering for `/api/events`. Hako does not trust
forwarded headers. The included `deploy/Caddyfile` is a minimal Caddy example;
replace `hako.example.com` with your hostname and configure DNS before use.

Download the archive for the server architecture and `SHA256SUMS` from the same
release. Verify the selected archive with `shasum -a 256` (macOS) or `sha256sum`
(Linux), comparing the complete digest with its entry in `SHA256SUMS`, then extract
it. Run the following commands from the extracted archive directory.

### Linux with systemd

```sh
sudo ./scripts/install-server.sh --public-url https://hako.example.com \
  --database-url-file /absolute/path/to/database.url
sudo systemctl status hako
sudo journalctl -u hako
```

The installer creates a dedicated `hako` user, installs the binary at
`/usr/local/libexec/hako/hako`, stores configuration in `/etc/hako/server.env`,
copies the private connection file to `/etc/hako/database.url` (0600, owned by
`hako`), and keeps the administrator key in `/var/lib/hako` (0700). Fleet data
lives in PostgreSQL. It starts a loopback listener at
`127.0.0.1:8787`. Configure your HTTPS proxy separately.

The administrator key is `/var/lib/hako/admin.key` (0600). Read it privately on
the server and enter it in the dashboard sign-in form. Never send it to workers.

### macOS with a user LaunchAgent

Install a persistent copy of the binary and customize the supplied template:

```sh
install -d -m 0700 "$HOME/Library/Application Support/Hako/server"
install -d -m 0755 "$HOME/.local/bin" "$HOME/Library/LaunchAgents"
install -m 0755 ./hako "$HOME/.local/bin/hako"
server_data="$HOME/Library/Application Support/Hako/server"
server_plist="$HOME/Library/LaunchAgents/moe.uwucocoa.hako.server.plist"
cp deploy/macos/moe.uwucocoa.hako.server.plist "$server_plist"
plutil -replace ProgramArguments.0 -string "$HOME/.local/bin/hako" "$server_plist"
plutil -replace ProgramArguments.3 -string "$server_data" "$server_plist"
plutil -replace ProgramArguments.7 -string 'https://hako.example.com' "$server_plist"
plutil -replace ProgramArguments.9 -string '/absolute/path/to/database.url' "$server_plist"
plutil -replace StandardOutPath -string "$server_data/server.log" "$server_plist"
plutil -replace StandardErrorPath -string "$server_data/server.log" "$server_plist"
launchctl bootstrap "gui/$(id -u)" "$server_plist"
```

The key is `$server_data/admin.key`. This service runs while your user session
is logged in. For an unattended server, prefer the Linux service above. Configure
HTTPS separately; the supplied template listens on loopback only.

### Foreground or development server

```sh
./hako serve --data-dir ./server-data --database-url-file ./database.url \
  --listen 127.0.0.1:8787
```

For explicit LAN listeners, repeat `--listen` with each concrete `IP:port`.
Wildcard addresses are rejected. Plain HTTP on a LAN is suitable only for a
trusted local development environment: administrator credentials and telemetry
are not transport-encrypted. Remote workers require HTTPS; only literal
loopback IPs allow HTTP. In local development, ignored `.local/listen-addresses`
can add listeners to `scripts/run-local.sh` without changing tracked source.

## Enroll a Mac

1. Sign in to the dashboard and choose **Add a Mac**. Optionally give it a name.
   Create a key, valid for 15 minutes by default (maximum 24 hours).
2. Save the key in a private file on the target Mac. Use an editor or password
   manager; avoid putting secrets into command arguments or shell history.
   Set the file permissions to 0600.
3. Download and verify the macOS archive matching `uname -m`: `arm64` for Apple
   silicon, `amd64` for Intel. Extract it and run:

```sh
sudo ./scripts/install-worker.sh \
  --server https://hako.example.com \
  --key-file /absolute/path/to/enrollment.key
```

The installer registers the worker, saves its private identity in
`/Library/Application Support/Hako/worker.json`, and installs the root
LaunchDaemon `moe.uwucocoa.hako.worker`. Its binary is
`/usr/local/libexec/hako/hako`; logs are `/Library/Logs/Hako/worker.log`.
Remove the enrollment-key input file after a successful join.

The key can claim exactly one UUID. A failed network response can be retried
using the same pending configuration and key. A successful join never silently
overwrites an existing worker identity. Do not copy `worker.json` between Macs.

For an ordinary-user foreground worker without a service or root privileges:

```sh
./hako join --server https://hako.example.com \
  --config ./worker.json --key-file ./enrollment.key
./hako agent --config ./worker.json --interval 2s
```

Some privileged fields will be unavailable. After the initial inventory, routine
CPU, memory, and network sampling does not require root.

### Issue keys from the server CLI

```sh
./hako invite --server http://127.0.0.1:8787 \
  --admin-key-file ./server-data/admin.key \
  --ttl 15m --label 'Build Mac' --key-out ./enrollment.key
./hako invitations --admin-key-file ./server-data/admin.key
./hako machines --admin-key-file ./server-data/admin.key
```

Run these as the account that owns the administrator key. `--key-out` creates a
private file and refuses an existing destination. Without it, `invite` writes
the enrollment key to standard output. Do not capture that output in shared logs.

## Rename, remove, and replace

Open a machine's **Device** tab to edit its display name. An empty name restores
the hostname. Neither operation changes the UUID, worker token, or Mac hostname.

**Remove machine** immediately revokes its token and removes its latest report.
The worker stops after the next rejected report. A revoked identity cannot be
restored by replaying its old enrollment key. To join again, stop the worker,
remove or archive its private configuration, issue a new key, and enroll a new
UUID. Revoking a pending enrollment key does not revoke an already joined Mac.

CLI removal uses `hako revoke --id DEVICE_UUID --admin-key-file ...`; add
`--invitation` with an invitation ID to revoke an enrollment key instead.

## Upgrade and roll back

Back up the server before an upgrade. Verify and extract the new release.
On Linux, rerun `sudo ./scripts/install-server.sh` without arguments. It preserves
`server.env`, `database.url`, and PostgreSQL data. On each Mac worker, rerun
`sudo ./scripts/install-worker.sh` without enrollment arguments. It preserves
the UUID and token and restarts the service with the new binary.

For the macOS server, stop its LaunchAgent, replace the persistent binary, and
bootstrap the same plist again. Do not overwrite a running executable in place.

Database migrations run on startup. A newer schema is rejected by an older
binary. Roll back the binary and its matching pre-upgrade backup together; never
point old software at a database with an unknown newer schema.

## Back up and restore

Use PostgreSQL's backup and restore tools, and back up the server's `admin.key`
separately. Follow [the database recovery procedure](postgresql.md#back-up-and-restore).
Copying the Hako data directory does not back up the fleet database.

## Stop or uninstall

On a worker, run `sudo launchctl bootout system/moe.uwucocoa.hako.worker` and
remove its plist from `/Library/LaunchDaemons`. On a macOS server, use
`launchctl bootout "gui/$(id -u)/moe.uwucocoa.hako.server"` and remove its plist.
On Linux, use `sudo systemctl disable --now hako`, remove the unit, and run
`sudo systemctl daemon-reload`.

Remove binaries and logs only after stopping services. Keep database and identity
files unless intentionally resetting the installation. Revoke a worker on its
server before disposing of its local configuration. Configure log rotation for
long-running installations; the supplied macOS services write ordinary log files.
