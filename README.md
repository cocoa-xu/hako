# Hako

Hako (箱, "box") is a personal Mac fleet management project, primarily for
Mac mini clusters.

The project is currently in the planning stage.

## Agreed direction

- Use Go as the primary implementation language.
- Install a small agent on each Mac, running as root.
- Have agents report machine information to a central service for a web UI.
- Collect and report fixed hardware inventory once on each agent startup,
  rather than periodically rescanning it.
- Treat the reference screenshots as examples of useful fields, not a UI design.

## Inventory and status

Fixed hardware inventory includes the model name, model identifier, model
number, chip, architecture, core counts, installed memory, serial number, and
hardware UUID. These belong in the startup report.

Changing information is collected separately: hostname, operating system
version and build, uptime, resource usage, storage, network interfaces, and
login state. Collection intervals remain to be decided.

Candidate fields also include firmware and OS loader versions, provisioning
UDID, Activation Lock, Remote Login, automatic login, and running session apps.
Availability and permissions must be verified on supported macOS versions.

Network inventory should represent multiple interfaces and, where available,
their LLDP neighbors: switch name, model, port, management address, and chassis
MAC address.

The reference fields "VM disk written" and "Host footprint" need explicit
definitions before implementation.

## Proposed implementation

These are starting points for discussion, not settled requirements:

- Run the agent as a launchd LaunchDaemon.
- Have agents initiate HTTPS connections to the service.
- Give each enrolled agent an independent identity and revocable credential.
- Use a Go server with embedded web assets and SQLite for the initial deployment.
- Retry unsuccessful startup reports and provide a manual inventory refresh.
- Track observation times and distinguish unavailable, unsupported, failed,
  and stale information from valid values.
- Start the UI with a searchable machine list and individual machine details.
- Store user-managed labels, locations, and notes separately from reported data.

## Open decisions

- Minimum macOS version and supported CPU architectures.
- Initial field set, dynamic collection intervals, and history retention.
- Enrollment, web authentication, and deployment details.
- LLDP collection strategy and whether session apps need a user-session helper.
- UI design and the scope of any future remote management actions.
