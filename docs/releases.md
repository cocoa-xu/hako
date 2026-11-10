# Release process

## Artifacts

The pinned toolchain is Go 1.27.3 and GoReleaser v2.20.0. Build the complete matrix
on macOS with Xcode Command Line Tools. Install GoReleaser from its
[official releases](https://github.com/goreleaser/goreleaser/releases/tag/v2.20.0)
and verify the download against the checksums from that release.

| Archive target | Server | Worker | Build |
| --- | --- | --- | --- |
| darwin_arm64 | Yes | Yes | Native Darwin CPU sampling, cgo |
| darwin_amd64 | Yes | Yes | Native Darwin CPU sampling, cgo |
| linux_amd64 | Yes | No | Pure Go |
| linux_arm64 | Yes | No | Pure Go |

macOS builds target macOS 13 or later. This is a build target, not a claim that
every supported OS or Mac model has been exercised. Live collection has been
validated on Apple silicon; Intel and older macOS require release smoke testing.

Each archive contains the binary, installation scripts, service templates,
documentation, and license notices. `SHA256SUMS` covers the archive bytes.
Runtime data, credentials, development logs, and signing keys are excluded.

See [validation notes](validation.md) for the checks actually performed.

## Local rehearsal

```sh
make test vet audit
make release-check
make snapshot
```

The snapshot command uses ad-hoc macOS signatures and never publishes artifacts
or submits them to Apple. Output is in ignored `dist/`. When no Git remote exists,
the local wrapper supplies an example URL only to the GoReleaser process for
metadata validation. It does not save a remote or contact that example repository.

Inspect archive contents, check `hako version`, verify signatures with
`codesign --verify --strict`, and exercise worker enrollment and server restart.
Installer layouts can be tested without root or starting services:

```sh
./scripts/install-worker.sh --staging-root /absolute/path/to/worker-stage
./scripts/install-server.sh --public-url https://hako.example.com \
  --staging-root /absolute/path/to/server-stage
```

Remove staging trees after review. Staging checks file layout, not service-manager
integration. Snapshots are development artifacts, not notarized public releases.

## Prepare a version

1. Complete tests and privacy review. Update `CHANGELOG.md` with the version and
   release date. Inspect dependency changes and update `licenses/` when needed.
2. Review current tracked files with `make audit`. Also inspect Git history and
   commit metadata before the first public push; the source audit checks the
   current tree only. Never export `.local/`, including backups and old JSON data.
3. Commit the release preparation and create a version tag such as `v0.1.0` only
   when that version is ready. The tag must point at the tested commit.
4. Back up any production server before deploying a new database migration.

## Apple signing and notarization

The signing identifier is `moe.uwucocoa.hako`. Keep the Developer ID Application
identity and notarization credentials in the local Keychain or CI secrets. They
are deliberately absent from source. Local `make sign` uses `SIGN_IDENTITY` and
makes no timestamp or notarization request.

A non-snapshot GoReleaser run requires `SIGN_IDENTITY` and a notarytool
`NOTARY_PROFILE`. It uses a hardened-runtime Developer ID signature with an Apple
timestamp, submits each zipped macOS binary for notarization, and waits for an
Accepted result before packaging. Optional `NOTARY_KEYCHAIN` selects a custom
keychain containing the profile. Notarization uploads executable code to Apple.

The binary inside a tar archive cannot carry a stapled ticket. Gatekeeper may
need network access to retrieve its notarization result. A future signed installer
package could support stapled offline distribution. Do not remove quarantine as
a substitute for validating a distributed release. See
[Apple's notarization guidance](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution).

## Future GitHub draft release

No repository creation or publication is part of local rehearsal. Once the owner
has chosen a GitHub repository and authorized publication:

1. Push the reviewed source and version tag to that repository.
2. Create a GitHub Actions environment named `release`. Configure its protection
   rules and the following secrets:

| Secret | Content |
| --- | --- |
| `SIGN_CERTIFICATE_BASE64` | Base64-encoded Developer ID Application `.p12` |
| `SIGN_CERTIFICATE_PASSWORD` | Export password for that certificate |
| `KEYCHAIN_PASSWORD` | Random password for the temporary CI keychain |
| `SIGN_IDENTITY` | Signing certificate identity or fingerprint |
| `NOTARY_KEY_BASE64` | Base64-encoded App Store Connect notarization API key |
| `NOTARY_KEY_ID` | API key ID |
| `NOTARY_ISSUER_ID` | API issuer ID |

3. Manually run **Draft release**, selecting the existing version tag. The job
   tests the tag, signs and notarizes macOS builds, creates all archives and
   checksums, and uploads them to a **draft** GitHub Release. It does not publish
   that draft. Failed notarization stops the job. Temporary signing material is
   removed even after a failure.
4. Download the draft artifacts, verify checksums and signatures, and smoke-test
   installation on representative worker and server hosts. Review release notes,
   then publish the draft explicitly when ready.

The workflow has no automatic tag-push release trigger. Action revisions and the
GoReleaser version are pinned. The design follows the
[GoReleaser Actions workflow](https://goreleaser.com/customization/ci/actions/)
and [GitHub's signing-keychain guidance](https://docs.github.com/en/actions/how-tos/deploy/deploy-to-third-party-platforms/sign-xcode-applications).

A signed public binary necessarily includes the signing certificate's publisher
identity. This is distinct from accidental account, hostname, or runtime data in
source and must be reviewed before distributing a signed build.
