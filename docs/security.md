# Security and API

## Identity and credentials

Every new worker creates a random UUIDv4 and an independent 256-bit bearer token.
The UUID is an identifier, not a secret. The hostname, serial number, hardware
UUID, MAC address, and display name are not used as credentials. Existing local
prototype IDs migrate deterministically to UUIDs so their identity remains
stable. Reinstalling with a new configuration creates a new identity.

The server stores SHA-256 digests of worker tokens and enrollment keys, and
persists key consumption and device creation in a single SQLite transaction.
Workers save a pending identity before contacting the server. Repeating exactly
the same claim after a lost response succeeds without consuming another identity;
a different claim fails. An unused key expires after its chosen lifetime,
between one minute and 24 hours. The default is 15 minutes.

A copied worker configuration can impersonate that same logical device. UUIDs
and single-use enrollment do not provide hardware attestation. Keep each
`worker.json` private and revoke the device if its token may have leaked.

The administrator key grants full fleet access. It is generated into `admin.key`
with mode 0600, never printed at server startup, and is separate from enrollment
and worker keys. Browser login exchanges it for a random HttpOnly, SameSite=Strict
session cookie lasting 12 hours. Session digests persist in SQLite. Logout revokes
the session. Configuring an HTTPS public origin also makes the cookie Secure.

To rotate the administrator key, stop the server, remove `admin.key`, and restart.
A new key is generated and existing sessions are invalidated. Worker credentials
remain valid. Preserve a private backup if administrator access must be recovered.

State directories use 0700; private files use 0600. The database is not encrypted
at rest. Use appropriate host access controls and encrypted storage. Never commit
runtime state, exports, logs, tokens, or signing material.

## Boundaries

- Workers may submit reports only for their authenticated device UUID.
- Enrollment keys can claim a device; they cannot read fleet data or issue keys.
- Administrator authentication protects fleet reads, live events, enrollment-key
  management, rename, and removal. Static sign-in assets and `/healthz` are public.
- Browser mutations require an exact same-origin Origin header when using a
  session cookie. The server restricts Host headers to configured listeners and
  the optional HTTPS public origin. Bearer-authenticated CLI calls need no Origin.
- Worker and management clients reject redirects and require HTTPS except for
  literal loopback addresses. They use the system certificate trust store.
- Removing a device persists a revocation tombstone and deletes its report in
  one transaction. A late or repeated old report cannot recreate it.
- Only one receiver process may serve a data directory. The database uses WAL,
  foreign keys, and synchronous FULL commits. Latest observations survive restart.

The receiver cannot ask workers to execute commands. Collection is read-only;
root access is for observing restricted system information. This version has one
administrator role, no multi-user RBAC, hardware attestation, or historical audit
log. Keep the service behind HTTPS and normal host/network access controls.

## API summary

JSON requests reject unknown fields and excess bodies. API errors use HTTP status
codes and plain-text messages. Enrollment and worker tokens use `Authorization:
Bearer TOKEN`. Administrator endpoints accept the admin bearer key or a session.

| Method | Path | Access | Purpose |
| --- | --- | --- | --- |
| GET | `/healthz` | Public | Process health |
| POST | `/api/session` | Admin bearer key | Create browser session |
| GET | `/api/session` | Admin | Check authentication |
| POST | `/api/logout` | Admin session | Log out |
| GET | `/api/machines` | Admin | Latest reported machines |
| GET | `/api/events` | Admin | Server-sent fleet updates |
| POST | `/api/enrollments` | Admin | Create key: `label`, `ttl_seconds` |
| GET | `/api/enrollments` | Admin | Key metadata, never raw keys |
| DELETE | `/api/enrollments/{id}` | Admin | Revoke an enrollment key |
| POST | `/api/enroll` | Enrollment key | Claim `device_id`, `token_hash` |
| POST | `/api/reports` | Worker token | Submit that worker's observation |
| PATCH | `/api/machines/{uuid}` | Admin | Set `display_name` |
| DELETE | `/api/machines/{uuid}` | Admin | Revoke device and delete snapshot |

A key is returned only by its creation response. Enrollment returns 204 for a
successful claim or exact retry, 401 for an unknown/revoked key, 409 for a key
already claimed by another identity, and 410 for an expired unused key. Device
names allow up to 80 Unicode characters without control characters. An empty
name uses the reported hostname. Worker reports rejected with 401 or 403 stop
the worker rather than retrying indefinitely.
