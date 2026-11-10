# Local validation

Release preparation checked on 2026-11-11. No GitHub release was created and no
notarization submission was made.

## Passed

- `go test -race ./...` and `go vet ./...`, including enrollment expiry, concurrent
  single-key claims, lost-response retries, credential revocation, browser sessions,
  origin restrictions, database persistence, legacy migration, and UUID rename.
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
- The existing local worker migrated to SQLite and a stable UUID. The updated
  server and worker resumed live reports; CPU and network collection succeeded,
  hardware inventory remained cached, and unauthenticated fleet reads were denied
  on both configured listeners.

## Still requires deployment validation

- Intel Mac live collection, older macOS versions, and actual root LaunchDaemon
  installation. Cross-compilation and plist checks do not replace these checks.
- Linux server execution and actual systemd installation. The available remote
  test host was unreachable during this run; Linux binaries were cross-compiled.
- Production DNS, HTTPS proxy configuration, and certificate issuance.
- Developer ID notarization and the GitHub draft-release workflow with real CI
  secrets. Local snapshot builds deliberately do not exercise external publishing.

The local development binary was signed with the configured Apple identity.
Snapshot archives retain ad-hoc signatures, without embedding that publisher's
certificate. Temporary UI test state and installer staging directories were
removed after validation.
