# Security and API

## Identity and credentials

Every new worker creates a random UUIDv4 and an independent 256-bit bearer token.
The UUID is an identifier, not a secret. The hostname, serial number, hardware
UUID, MAC address, and display name are not used as credentials. Existing local
prototype IDs migrate deterministically to UUIDs so their identity remains
stable. Reinstalling with a new configuration creates a new identity.

The server stores SHA-256 digests of worker tokens and enrollment keys, and
persists key consumption and device creation in a single PostgreSQL transaction.
Workers save a pending identity before contacting the server. Repeating exactly
the same claim after a lost response succeeds without consuming another identity;
a different claim fails. An unused key expires after its chosen lifetime,
between one minute and 24 hours. The default is 15 minutes.

A copied worker configuration can impersonate that same logical device. UUIDs
and single-use enrollment do not provide hardware attestation. Keep each
`worker.json` private and revoke the device if its token may have leaked.

In the default builtin authentication mode, the administrator key grants full
fleet access. It is generated into `admin.key`
with mode 0600, never printed at server startup, and is separate from enrollment
and worker keys. Browser login exchanges it for a random HttpOnly, SameSite=Strict
session cookie lasting 12 hours. Session digests persist in PostgreSQL. Logout revokes
the session. Configuring an HTTPS public origin also makes the cookie Secure.

To rotate the administrator key, stop the server, remove `admin.key`, and restart.
A new key is generated and existing sessions are invalidated. Worker credentials
remain valid. Preserve a private backup if administrator access must be recovered.

`--admin-auth external` delegates administrator access to the upstream proxy or
trusted network. All requests reaching Hako's management routes have administrator
access in this mode; no Cloudflare header or JWT validation is performed by Hako.
No local login, logout, or new session is available. Enrollment and worker report
authentication are unchanged. The setting is explicit and does not persist in
the database. Protect every route and direct listener with the intended access
policy; limit worker Service Tokens to `/api/enroll` and `/api/reports`.

Optional Cloudflare Access Service Tokens are stored in each private worker
configuration and sent on registration and every report, alongside the Hako
Bearer credential. They are not sent to redirect destinations. Configure or
rotate them with `configure-access` without changing the Hako identity. They
are not included in inventory or report payloads.

Local state directories use 0700; private files use 0600. Database storage and
backups follow the PostgreSQL host configuration. Use a dedicated database role,
verified TLS for remote connections, and appropriate encrypted storage. Never commit
runtime state, exports, logs, tokens, or signing material.

## Boundaries

- Workers may submit reports only for their authenticated device UUID.
- Enrollment keys can claim a device; they cannot read fleet data or issue keys.
- Administrator authentication protects fleet reads, live events, enrollment-key
  management, rename, and removal. External mode delegates this boundary upstream.
  Static assets and `/healthz` are public at Hako's listener.
- Browser mutations require an exact same-origin Origin header when using a
  session cookie. The server restricts Host headers to configured listeners and
  the optional HTTPS public origin. Wildcard listeners allow literal IP Host
  headers, not arbitrary DNS names. Bearer-authenticated CLI calls need no Origin.
  External-mode mutations without Origin require `X-Hako-Request: cli`; this is
  a cross-origin request safeguard, not an authentication credential.
- Worker and management clients reject redirects and require HTTPS except for
  literal loopback addresses unless `--allow-http` explicitly permits private
  network HTTP. They use the system certificate trust store for HTTPS.
- Removing a device persists a revocation tombstone and deletes its report in
  one transaction. A late or repeated old report cannot recreate it.
- Only one receiver may own a PostgreSQL database. A session advisory lock is
  held on the same connection used for all reads and writes. Losing that session
  does not silently reconnect with stale cached credentials; the receiver exits
  and its service manager can restart it. Foreign keys, transactions, and
  synchronous commits protect persisted state. Latest observations survive restart.

The receiver cannot ask workers to execute commands. Collection is read-only;
root access is for observing restricted system information. This version has one
administrator role, no multi-user RBAC, hardware attestation, or historical audit
log. Keep the service behind HTTPS and normal host/network access controls.

## API summary

Management requests reject unknown fields; request bodies have size limits. API errors use HTTP status
codes and plain-text messages. Enrollment and worker tokens use `Authorization:
Bearer TOKEN`. Administrator endpoints accept the admin bearer key or a session.
In external mode those endpoints trust upstream access control instead, and
`POST /api/session` and `POST /api/logout` return 404. `GET /api/session` keeps
its 204 response and reports the mode in `X-Hako-Admin-Auth`.

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
