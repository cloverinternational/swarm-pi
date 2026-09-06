# SwarmOS TUI release automation

SwarmOS TUI source remains in the private `Swarm-Code/mono` monorepo. Stable
executables are distributed anonymously from the public
[`Swarm-Code/swarm-releases`](https://github.com/Swarm-Code/swarm-releases)
repository.

## Version and tag contract

The authoritative version is `internal/version/version.go` and must have the
form `vMAJOR.MINOR.PATCH`.

| Boundary | Example |
| --- | --- |
| Canonical source version | `v1.23.5` |
| Annotated private source tag | `tui/v1.23.5` |
| Public distribution tag | `v1.23.5` |

Run `ci/release/tui.sh version` from the monorepo root to print the canonical
version. `ci/release/tui_test.sh` verifies tag parsing, annotated-tag
enforcement, and the historical double-`v` regression.

`scripts/release.sh` never publishes. It can validate a clean `main`, build a
local Linux candidate, and create the annotated tag locally for review.

## CI release flow

`.github/workflows/release-tui.yml` has three entry points:

- Relevant pull requests build and validate without publishing.
- Manual dispatch builds and validates without publishing.
- A pushed `tui/v*` tag publishes only after metadata validation and every
  native build succeeds.

The stable matrix is:

- Linux amd64 on `ubuntu-24.04`
- Linux arm64 on `ubuntu-24.04-arm`
- macOS amd64 on `macos-15-intel`
- macOS arm64 on `macos-15`
- Windows amd64 on `windows-2025`

Each native runner builds with Go 1.26, CGO and FTS5, injects the canonical
version and exact source commit, executes `--version`, and emits a raw
executable plus a SHA-256 file. The publisher requires the complete matrix and
verifies every checksum before creating the public release.

Release assets are immutable:

```text
swarmos-linux-amd64
swarmos-linux-amd64.sha256
swarmos-linux-arm64
swarmos-linux-arm64.sha256
swarmos-darwin-amd64
swarmos-darwin-amd64.sha256
swarmos-darwin-arm64
swarmos-darwin-arm64.sha256
swarmos-windows-amd64.exe
swarmos-windows-amd64.exe.sha256
checksums.txt
dependencies.txt
sbom.spdx.json
provenance-attestation.jsonl
sbom-attestation.jsonl
```

If publication or post-publication verification fails, fix the pipeline and
release a new patch version. Never replace an executable under an existing
public tag.

The SPDX document covers the complete release candidate directory. GitHub
Actions OIDC and Sigstore sign both SLSA provenance and SPDX SBOM attestations
for all five executables. The signer deliberately skips GitHub repository API
persistence because that feature is unavailable for this private repository's
plan. The signed bundles are public release assets and can be verified offline
without access to the private source repository.

If an approved public-repository workflow performs an emergency build, its
certificate and builder identity remain that public workflow. Its provenance
must separately identify `Swarm-Code/mono`, the fetched annotated source tag,
and the exact checked-out source commit as the resolved dependency. Decode and
inspect the signed predicate before accepting the immutable release.

## Publisher identity

The publish job obtains a short-lived token from an organization-owned GitHub
App. The App must:

- be installed only on `Swarm-Code/swarm-releases`;
- have repository metadata read and contents read/write;
- have no webhook and no organization-wide write permissions.

Private `mono` Actions configuration:

- variable `RELEASE_APP_CLIENT_ID`
- secret `RELEASE_APP_PRIVATE_KEY`

Build jobs receive neither this identity nor write permissions. If the App
configuration is missing, candidate builds remain available to CI but the
publish job fails closed.

## Creating a stable release

1. Update the canonical version and public release notes.
2. Merge the reviewed release change after the Go and five-platform build
   checks pass.
3. From a clean, current `main`:

   ```bash
   cd swarm-sdk/swarm-tui
   ./scripts/release.sh check
   ./scripts/release.sh tag
   git push origin tui/v1.23.5
   ```

4. Inspect every workflow job. Do not publish around a failed matrix job.
5. Verify the public release anonymously:

   ```bash
   curl -fsSL \
     https://api.github.com/repos/Swarm-Code/swarm-releases/releases/latest
   ```

6. Download the matching executable and `.sha256` file, run
   `sha256sum -c`, then execute `--version`.
7. Download both attestation bundles and verify them offline:
   ```bash
   gh attestation verify swarmos-linux-amd64 \
     --bundle provenance-attestation.jsonl \
     --repo Swarm-Code/mono \
     --signer-workflow Swarm-Code/mono/.github/workflows/release-tui.yml
   gh attestation verify swarmos-linux-amd64 \
     --bundle sbom-attestation.jsonl \
     --repo Swarm-Code/mono \
     --signer-workflow Swarm-Code/mono/.github/workflows/release-tui.yml \
     --predicate-type https://spdx.dev/Document/v2.3
   ```

## Updater contract

The TUI updater queries the public repository and therefore does not require a
GitHub token. It selects only the exact platform executable and checksum names
above. Missing, duplicate, malformed, or mismatched release assets fail closed.
The stable channel follows GitHub's `/releases/latest` endpoint.

## Rollback

GitHub Releases are immutable distribution records, not deployment slots. To
recover from a bad release:

1. mark the affected release in its notes;
2. fix and verify the source on `main`;
3. bump the patch version;
4. publish a new annotated source tag.

Do not move source tags, force-push release refs, or replace public assets.
