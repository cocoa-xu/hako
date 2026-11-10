# Data collection

## Inventory and sampling

| Category | Fields |
| --- | --- |
| Hardware | Model name, identifier and number; chip; architecture; physical and logical cores; every reported CPU core group; installed memory |
| Device identity | Serial number, hardware UUID, provisioning UDID |
| Platform | Firmware, OS loader, Activation Lock as reported by System Information |
| System | Hostname, macOS version and build, Rapid Security Response suffix when present, boot time, uptime, 1/5/15-minute load averages, console user |
| CPU | Sampled CPU usage with 100% per logical core, logical core count |
| Memory | Free, active, inactive, speculative, wired and compressed pages in bytes; kernel memory pressure; swap total and usage |
| Storage | Startup data volume path, total capacity, free bytes and available bytes |
| Network | Interface names, MAC addresses, IP prefixes and flags; Ethernet / Wi-Fi received and sent byte counters; average download and upload rates |
| Access | Remote Login status, FileVault status, configured automatic-login user |

Hardware is collected on agent startup and sent with the first successful
report. Failed deliveries reuse the same observation. Subsequent reports contain
dynamic state only. If the receiver loses its saved inventory, it requests the
cached copy instead of triggering another hardware scan. An unsuccessful
hardware collection is retried. Restart the agent to refresh hardware manually.

Dynamic collection runs on a fixed two-second cadence by default. Collection
and delivery are independent: a slow receiver keeps only the newest pending
sample. Security settings are checked every 30 seconds; hardware is still
collected at startup. Individual commands have timeouts.

The dashboard receives reports through a server-sent event stream and reconnects
automatically. It shows no current network speed after ten seconds without a
new traffic observation. A machine has no recent report after 90 seconds without
receipt. These indicate reporting freshness, not proof that the Mac is off.

Unknown, unsupported, permission-denied, and failed observations do not become
zero or false. Each section records its observation time. A failed dynamic
section is displayed as unavailable rather than silently reusing an old value.

## Data semantics

- CPU groups use system-provided names, including systems with more than two
  groups. No fixed performance/efficiency split is assumed.
- Load average is not CPU utilization.
- CPU usage uses differences in Darwin `HOST_CPU_LOAD_INFO` ticks across
  consecutive samples, including user, system, and nice time. One busy logical
  core is 100%; twelve fully busy cores are 1200%. The first observation waits
  for a second sample instead of inventing a rate. The default macOS build uses
  cgo with Apple's SDK and requires no root privileges. Builds with cgo disabled
  retain the `top` fallback, which measures a one-second window per report.
  The counter definitions follow [Apple's host statistics API](https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/mach/host_info.h).
- The CPU column shows physical core groups in E / P / S order (Efficiency,
  Performance, Super). Absent groups are omitted; unknown groups keep their
  system-provided names. Macs without group data show their physical core count
  and, when different, logical thread count. The CPU cell has two lines: usage
  bar with percentage, then chip model and core configuration together. CPU,
  memory, and storage bars align horizontally; network follows storage.
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
  Available space remains a separate value in the detail view. The table bar
  changes color below 10% available, with a stronger warning below 5%.
- Network totals use 64-bit link-layer `Ibytes` / `Obytes` counters from
  [macOS netstat](https://github.com/apple-oss-distributions/network_cmds/blob/main/netstat.tproj/if.c).
  Each `en` interface is counted once, including down interfaces. IP address
  rows, loopback, tunnels, bridges, and peer-to-peer interfaces are excluded
  to avoid counting traffic twice. These totals include LAN traffic. They
  normally accumulate since boot, survive an agent restart, and can reset when
  an interface resets or is recreated; removing an interface removes its
  contribution. This is not an internet-provider billing counter.
- Download / upload rates divide counter deltas by the actual elapsed sampling
  time. The first sample, a counter decrease, or an interface-set change leaves
  speed unavailable until the next valid interval. A failed read clears the
  rate baseline. Totals and rates use decimal B / KB / MB / GB and bytes/sec.
- Interface "Up" is the OS interface flag, not proof of internet connectivity.
- Automatic login reflects the configured preference, not a guarantee that the
  next login will be automatic.
- Activation Lock is a startup observation, explicitly labeled as such in the
  UI. It can change while the agent is running; restart to refresh it.

