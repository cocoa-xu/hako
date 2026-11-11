# Changelog

## Unreleased

- Support per-worker Cloudflare Access Service Tokens for enrollment and reports.
- Add Access credential rotation without replacing an existing worker identity.
- Document Cloudflare Access policies and nginx event-stream proxying.

## 0.1.0 - 2026-11-11

- Add a Mac fleet dashboard with live CPU, memory, storage, and network metrics.
- Sample every two seconds and push updates through a reconnecting event stream.
- Keep startup hardware inventory separate from dynamic observations.
- Add administrator sessions, expiring single-use enrollment keys, and immediate worker revocation.
- Persist device UUIDs, editable display names, enrollment state, sessions, and latest reports in PostgreSQL.
- Migrate existing local credentials and snapshots without replacing installation identities.
- Add deployment templates, installation scripts, release checksums, and a draft release workflow.
