# Hako

Hako (箱, "box") is a small, self-hosted dashboard for a personal Mac fleet,
primarily Mac mini clusters. The Go binary contains the collector, reporting
agent, local receiver, and web UI. There are no third-party Go dependencies or
frontend build dependencies.

The first working version supports local development and inspection. The web
receiver defaults to loopback and can also listen on explicitly selected LAN
addresses. Web login and TLS termination are not implemented yet; any client
that can reach a configured address can view the dashboard and machine data.

## Run locally

Use Go 1.27.3, pinned in `.go-version`:

```sh
make build
./scripts/run-local.sh
```

If Go is installed in `~/sdk/go1.27.3` and is not on `PATH`, build with
`make GO="$HOME/sdk/go1.27.3/bin/go" build`.

Open <http://127.0.0.1:8787>. The script starts the receiver and agent, reuses the
local device identity, and stops both when you press Control-C.

Selecting a machine opens a detail drawer from the right while keeping the fleet
on the same page. The drawer occupies roughly a third of a desktop window and
the full width on a phone. Overview, Network, and Device tabs share an independent
scroll area; firmware and identifiers expand on demand. Close with the X, Escape,
or a click outside. Focus returns to the selected machine.

To also listen on a LAN address, add one `IP:port` per line to the ignored
`.local/listen-addresses` file. For example:

```sh
mkdir -p .local
printf '%s\n' '192.0.2.10:8787' > .local/listen-addresses
./scripts/run-local.sh
```

The script retains `127.0.0.1:8787` for local reporting. The receiver also accepts
repeated `--listen` flags directly:

```sh
./bin/hako serve --data-dir .local \
  --listen 127.0.0.1:8787 --listen 192.0.2.10:8787
```

### Demo fleet

Open `/?demo=1` on the running dashboard, or select **View demo**, to preview eight
sample Mac minis. The demo includes different chips and capacities, memory
pressure, low available storage, missing observations, and stale reports. Search,
machine selection, and all detail sections use the same UI as the live fleet.
Select **Back to live** to return.

Demo data is generated in the browser and never submitted to the receiver or
saved in the live inventory. Sample identifiers and documentation-only IP
addresses are fictional. The live agent continues reporting independently.

### Listener and collection options

Wildcard binds are rejected. All requested addresses must bind successfully
before serving, and requests still require an allowed Host and same-origin
browser access. Agent credentials remain required for submitting reports.

To include collection that requires root:

```sh
./scripts/run-local.sh --root-agent
```

Only the collector runs under `sudo`; the web receiver stays under the invoking
user. The collector reads system state and reports it. It does not change system
settings, install updates, or accept commands from the receiver. Passwords are
never stored. No boot service is installed by this script.

For a single JSON report without a receiver:

```sh
./bin/hako inspect
sudo ./bin/hako inspect
```

Inspection output includes device identifiers and network addresses. Keep any
saved reports private.

## Signing

The binary can be signed with an existing local Apple signing identity:

```sh
security find-identity -v -p codesigning
SIGN_IDENTITY='Developer ID Application: Your Name (TEAMID)' make sign
```

This performs local signing and verification without notarization or a timestamp
request. Build before signing: rebuilding replaces the signature. Signing does
not grant root access; the root collection mode still uses `sudo`.

## What is collected

| Category | Fields |
| --- | --- |
| Hardware | Model name, identifier and number; chip; architecture; physical and logical cores; every reported CPU core group; installed memory |
| Device identity | Serial number, hardware UUID, provisioning UDID |
| Platform | Firmware, OS loader, Activation Lock as reported by System Information |
| System | Hostname, macOS version and build, Rapid Security Response suffix when present, boot time, uptime, 1/5/15-minute load averages, console user |
| CPU | Sampled CPU usage with 100% per logical core, logical core count |
| Memory | Free, active, inactive, speculative, wired and compressed pages in bytes; kernel memory pressure; swap total and usage |
| Storage | Startup data volume path, total capacity, free bytes and available bytes |
| Network | Every interface's name, MAC address, IP prefixes, administrative up flag and loopback flag |
| Access | Remote Login status, FileVault status, configured automatic-login user |

The original screenshots are field references, not a UI specification.

Hardware is collected on agent startup and sent with the first successful
report. Failed deliveries reuse the same observation. Subsequent reports contain
dynamic state only. If the receiver loses its saved inventory, it requests the
cached copy instead of triggering another hardware scan. An unsuccessful
hardware collection is retried. Restart the agent to refresh hardware manually.

Dynamic collection runs immediately, then waits 30 seconds between reports by
default. Individual commands have timeouts. Hardware does not use that interval.
The dashboard refreshes every five seconds and marks a machine as having no
recent report after 90 seconds without receipt. This indicates reporting
freshness, not proof that the Mac is powered off.

Unknown, unsupported, permission-denied, and failed observations do not become
zero or false. Each section records its observation time. A failed dynamic
section is displayed as unavailable rather than silently reusing an old value.

## Data semantics

- CPU groups use system-provided names, including systems with more than two
  groups. No fixed performance/efficiency split is assumed.
- Load average is not CPU utilization.
- CPU usage comes from the second `top` summary, with a one-second sampling
  delay. The first sample is discarded. User and system CPU time are summed and
  scaled by the logical core count, so one busy core is 100% and twelve fully
  busy cores are 1200%. This is a short sample per report, not an average over the
  reporting interval. Collection requires no root privileges.
- The CPU column shows physical core groups in E / P / S order (Efficiency,
  Performance, Super). Absent groups are omitted; unknown groups keep their
  system-provided names. Macs without group data show their physical core count
  and, when different, logical thread count. The CPU cell has three lines: usage
  bar and percentage, chip model, then core configuration.
- Its bar is scaled to total CPU capacity: 600% fills half the bar on a twelve-core
  Mac, whose maximum is 1200%. Missing samples show no bar.
- Free pages are not presented as total available memory. The UI pairs memory
  pressure with the underlying page counters and swap usage.
- The fleet table shows a memory bar and used / free values in decimal GB.
  Free is the reported free-page count; used is installed memory minus free,
  including cached and inactive memory. The bar also includes those other used
  pages alongside active, wired, and compressed memory.
- The storage figure describes the startup data filesystem, not physical SSD
  capacity or a sum of APFS volumes. Available space does not include a custom
  estimate of purgeable files.
- The fleet table shows a storage bar and used / free values in decimal GB.
  Used is total minus filesystem-reported free space, matching the detail bar.
  Available space remains a separate value in the detail view.
- Interface "Up" is the OS interface flag, not proof of internet connectivity.
- Automatic login reflects the configured preference, not a guarantee that the
  next login will be automatic.
- Activation Lock is a startup observation, explicitly labeled as such in the
  UI. It can change while the agent is running; restart to refresh it.

## Receiver and credentials

Run the two processes independently when useful:

```sh
./bin/hako serve --data-dir .local --listen 127.0.0.1:8787
./bin/hako enroll --data-dir .local --server http://127.0.0.1:8787
./bin/hako agent --config /path/printed/by/enroll.json --interval 30s
```

Enrollment creates a random device ID and independent bearer token. The device
ID does not depend on the hostname or MAC address. Remove the corresponding
credential file from the receiver's `agents/` directory to revoke it. The agent
requires HTTPS except for literal loopback IP addresses and does not follow
HTTP redirects.

Runtime state is kept in the ignored `.local/` directory. Credential and machine
files have mode 0600; state directories are created with mode 0700. Latest
snapshots are saved atomically as one JSON file per device and survive receiver
restarts. This first version does not retain historical samples. SQLite remains
an option for later history and query requirements.

## Validation

```sh
make test
```

Tests cover parsing, differing VM page sizes, three CPU groups, unavailable
privileged data, inventory delivery and retries, redirects, credential
revocation, receiver restart persistence, request limits, and browser-origin
checks. Fixtures use synthetic hardware identifiers.

The first version was also exercised on an Apple M6 Mac mini running macOS
27.0.1: ordinary-user and root collection, repeated reports with an unchanged
inventory timestamp, local signing verification, and real-browser desktop and
400-pixel layout checks. Only this host has been tested with live collection.

## Next decisions

- LLDP reception and a user-session app collector. Both are explicitly marked
  as not collected in the current UI.
- Minimum macOS version and additional hardware validation, particularly Intel.
- Remote receiver deployment, web authentication, and enrollment UX.
- LaunchDaemon packaging, installation, upgrades, and removal.
- Optional history, labels, locations, and notes.
- Definitions for the reference fields "VM disk written" and "Host footprint".
- Software update checks and any future remote management actions.
