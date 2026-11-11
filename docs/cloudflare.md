# Cloudflare Access

These options require a build newer than v0.1.0. The v0.1.0 worker cannot send
Access Service Token headers. Upgrade the worker before protecting its endpoint.

## Access policies and origin

Use an identity allowlist for the dashboard. Keep Hako's built-in administrator
login enabled unless deliberately delegating all administrator access to the
proxy. Every worker still needs a Hako enrollment key and its own worker token.
The optional `--admin-auth external` setting is described in
[administrator authentication](deployment.md#delegate-administrator-authentication).

Create a separate Cloudflare Access Service Token for each Mac. Authorize those
tokens with a **Service Auth** policy covering `/api/enroll` and `/api/reports`.
Use path-specific Access applications to keep worker tokens out of dashboard
and management routes. Do not put an interactive login or a Bypass policy on
the worker paths. See [Cloudflare's service token documentation](https://developers.cloudflare.com/cloudflare-one/access-controls/service-credentials/service-tokens/).

Configure the server's `--public-url https://hako.example.com` to match the
browser-facing origin. Preserve the original Host header through the tunnel
and proxy. The [nginx example](../deploy/nginx.conf) listens on loopback for a
local tunnel that terminates HTTPS; it disables buffering and caching for the
event stream. Adapt the ports to the deployment. The example does not terminate
TLS itself. The existing [Caddy example](../deploy/Caddyfile) is another option.

Restrict origin access to the intended tunnel or trusted network. For Cloudflare
Tunnel, enable **Protect with Access** so cloudflared validates Access tokens.
See [Cloudflare's origin setup](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/).

## New workers

Save each Mac's Service Token in a private JSON file with mode 0600:

```json
{
  "client_id": "YOUR_CLIENT_ID.access",
  "client_secret": "YOUR_CLIENT_SECRET"
}
```

Keep the actual values out of command arguments, shared logs, and source control.
Pass the file alongside the separate Hako enrollment key:

```sh
sudo ./scripts/install-worker.sh \
  --server https://hako.example.com \
  --key-file /absolute/path/to/enrollment.key \
  --access-service-token-file /absolute/path/to/access.json
```

For a foreground worker, use the same `--access-service-token-file` option with
`hako join --server ... --config ./worker.json --key-file ...`.

Registration and every report send both `CF-Access-Client-Id` and
`CF-Access-Client-Secret`. Hako's `Authorization: Bearer` header stays separate.
No browser login or Access cookie is needed. Redirects are rejected rather than
forwarding credentials to a login page or another host.

The Access pair is saved under `cloudflare_access` in the private worker JSON,
including pending enrollment retries. Remove the input token file after safely
storing the credentials; it is not needed for subsequent reports. The Hako
enrollment key is not persisted in worker configuration.

Administrator CLI commands also accept `--access-service-token-file`. Use an
administrator-specific Access token authorized for the management routes and,
in builtin mode, `--admin-key-file` as well.

## Existing workers and token rotation

Upgrade the binary, then rerun the installer with only the Access token file:

```sh
sudo ./scripts/install-worker.sh \
  --access-service-token-file /absolute/path/to/access.json
```

This updates Access credentials and restarts the service while preserving the
Hako UUID, worker token, and server URL. Do not re-enroll an existing worker just
to add or rotate Access credentials. For a manually managed worker:

```sh
./hako configure-access --config ./worker.json \
  --service-token-file ./access.json
```

Restart the worker after updating its configuration. To remove the Access pair
when moving away from Access, use `configure-access --config ... --remove`.

Revoking a Mac's Cloudflare token blocks that Mac at Access. Hako's **Remove
machine** action separately revokes the worker identity. An HTTP 401 or 403
stops the worker; check both authentication layers before restarting it. An
Access token renewal does not require changing the Hako identity.

## Deployment check

Before switching the production hostname, use a test Mac and staging Access
policies. Confirm that a valid Service Token can enroll and report, a missing or
revoked token cannot, and a worker token cannot reach administrator routes.
Check that the dashboard's event stream updates without proxy buffering.
Local tests emulate the Access headers and failures; they do not validate a
particular Cloudflare account's deployed policies.
