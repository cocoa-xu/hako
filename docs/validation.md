# Local validation

Validation recorded on 2026-11-11. Completed artifact checks below cover v0.1.0;
Access and external authentication checks cover the v0.2.0 source.

## Passed

- `go test -race ./...` and `go vet ./...`, including enrollment expiry, concurrent
  single-key claims, lost-response retries, credential revocation, browser sessions,
  origin restrictions, database persistence, legacy migration, and UUID rename.
  Server tests run against real PostgreSQL 18.6, with isolated databases.
- PostgreSQL receiver ownership, newer-schema rejection, import rollback, and
  a terminated database session. A stale receiver cannot reconnect and undo
  a device revocation. Database health correctly reports an unavailable session.
- JavaScript syntax, shell syntax, macOS plist validation, and whitespace checks.
- Source privacy audit and a separate reachable-history scan for local runtime
  identifiers, private addresses, home-directory paths, and signing-account data.
  Git author/committer metadata intentionally follows the owner's configured
  identity. Third-party license notices retain their required attribution.
- GoReleaser configuration and local snapshot builds for Darwin arm64/amd64 and
  Linux arm64/amd64. Four archive checksums and archive contents verified.
- Both macOS binaries have valid ad-hoc snapshot signatures, correct architectures,
  and a macOS 13.0 build target. Binary strings contain no local user paths.
- Worker and server installers staged into disposable directories with their
  expected file layout and modes. No privileged service was installed.
- Real-browser desktop and 400-pixel enrollment views. Login, session logout,
  one-time key creation, pending-key revocation, and display-name editing worked.
  The rename preserved UUID and hostname. The removal prompt explains revocation.
  API removal rejected subsequent reports from the disposable worker.
- The existing SQLite state migrated to PostgreSQL without changing UUIDs,
  worker credential digests, names, or revocation states. The updated
  server and worker resumed live reports; CPU and network collection succeeded,
  hardware inventory remained cached, and unauthenticated fleet reads were denied
  on both configured listeners. The application uses a non-superuser database role.
- A live PostgreSQL logical backup restored into a separate disposable database
  with matching device identities and credential digests.
- The published v0.1.0 archives were downloaded from GitHub and their checksums
  verified. Both macOS binaries passed Developer ID signature and online
  notarization checks. The native Apple silicon binary reported the expected
  version. All five public release assets were accessible without authentication.
- Access Service Token headers on enrollment, pending retries, and reports;
  credential rotation preserving the worker identity; private file permissions;
  and rejection of redirects without forwarding Hako or Access credentials.
- Builtin and external administrator modes, origin checks, session behavior,
  live events, and unchanged worker enrollment and revocation with PostgreSQL.
  Wildcard IPv4 listening and literal-IP Host handling retain DNS rebinding and
  cross-origin rejection. Private-network HTTP requires explicit opt-in.
- Updated installers staged without root. Reruns preserve the listen address,
  authentication mode, and database connection when unrelated settings change.
  The nginx example passed an isolated configuration syntax check.

## Still requires deployment validation

- Intel Mac live collection, older macOS versions, and actual root LaunchDaemon
  installation. Cross-compilation and plist checks do not replace these checks.
- Actual Linux systemd installation. The public CI run passed tests, including
  PostgreSQL server integration tests, on Linux and macOS; a full systemd
  installation has not yet been exercised.
- Production DNS, HTTPS proxy configuration, and certificate issuance.
- Real Cloudflare Access identity and Service Auth policies, per-Mac token
  revocation, and an event stream through the deployed proxy. Header tests use
  local HTTP test servers, not a live Cloudflare account.

The local development binary was signed with the configured Apple identity.
PostgreSQL snapshot archives retain ad-hoc signatures, without embedding that
publisher's certificate. Temporary UI test state and installer staging directories were
removed after validation.
