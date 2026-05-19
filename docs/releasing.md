# Releasing Memora Core

Memora Core ships from tag pushes. The `Release` GitHub Actions
workflow (`.github/workflows/release.yml`) takes care of cross-platform
binary builds, SBOM generation, signing, Docker images, and (when
their tap repos exist) Homebrew and Scoop packages.

## TL;DR

```bash
# On main, after the release-worthy commit landed:
git tag -s v0.1.0 -m "Memora Core v0.1.0"
git push origin v0.1.0
```

The workflow runs goreleaser, which:

1. Cross-compiles `memora-core` + `memora-cli` to
   `linux × {amd64, arm64}`, `darwin × {amd64, arm64}`,
   `windows × {amd64, arm64}` — six platforms × two binaries = 12
   build outputs.
2. Archives each platform into `memora_<version>_<os>_<arch>.{tar.gz,zip}`
   bundled with `LICENSE`, `NOTICE`, `README.md`, `SECURITY.md`,
   `CONTRIBUTING.md`.
3. Generates an SPDX-JSON SBOM per archive via
   [syft](https://github.com/anchore/syft) so downstream consumers can
   verify the dependency graph.
4. Signs `checksums.txt` and the multi-arch Docker manifests with
   [cosign keyless](https://docs.sigstore.dev/cosign/signing/overview/)
   via the workflow's GitHub OIDC identity (no maintained private
   keys; ephemeral certificates witnessed by Rekor).
5. Builds and pushes `ghcr.io/axiom-studio/memora-core:<version>`
   (and `:latest`) as a multi-arch image (`linux/amd64` +
   `linux/arm64`) with full OCI labels.
6. Creates the GitHub Release with the auto-generated changelog and
   uploads every artifact.

## Prerequisites

### One-time repository setup

Before the first tag push, configure:

1. **GHCR push access** — the workflow uses the default
   `secrets.GITHUB_TOKEN` with the `packages: write` permission;
   nothing to do beyond ensuring the repo's package settings allow
   it (Settings → Actions → General → Workflow permissions: "Read and
   write permissions").
2. **Sigstore keyless signing** — works out of the box. The workflow
   has `id-token: write` permission, which is all cosign-keyless
   needs from the repo side. No secrets required.
3. *(Optional)* **Homebrew tap** — create a sibling
   `axiom-studio/homebrew-tap` repo (one-time). Then:
   - Generate a fine-grained PAT with `contents: write` on that repo.
   - Add it as `HOMEBREW_TAP_TOKEN` in this repo's Actions secrets.
   - The next release will commit a `Formula/memora.rb` to the tap repo.
4. *(Optional)* **Scoop bucket** — same pattern with
   `axiom-studio/scoop-bucket` and `SCOOP_BUCKET_TOKEN`.

When the optional tokens are absent, goreleaser's `skip_upload: auto`
quietly omits those steps — the release succeeds without them.

### Local validation before tagging

```bash
# Install goreleaser:
brew install goreleaser

# Dry-run with no publishing:
goreleaser release --snapshot --clean --skip=publish,sign

# The built artifacts land under ./dist/ and can be smoke-tested.
```

`--skip=sign` is necessary in local runs because cosign keyless
requires the CI's OIDC token; running locally would prompt for an
OAuth-issuer login that isn't useful for a snapshot.

## Verifying a release as a downstream consumer

```bash
# Pick a release artifact and its sigstore bundle:
TAG=v0.1.0
curl -LO https://github.com/axiom-studio/memora/releases/download/$TAG/checksums.txt
curl -LO https://github.com/axiom-studio/memora/releases/download/$TAG/checksums.txt.sig
curl -LO https://github.com/axiom-studio/memora/releases/download/$TAG/checksums.txt.cert

# Verify the signature chain:
cosign verify-blob \
  --certificate=checksums.txt.cert \
  --signature=checksums.txt.sig \
  --certificate-identity-regexp="https://github.com/axiom-studio/memora/.github/workflows/release.yml@refs/tags/$TAG" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  checksums.txt

# Then verify your downloaded archive against the now-trusted checksums file:
sha256sum -c checksums.txt --ignore-missing
```

For Docker images:

```bash
cosign verify ghcr.io/axiom-studio/memora-core:$TAG \
  --certificate-identity-regexp="https://github.com/axiom-studio/memora/.github/workflows/release.yml@refs/tags/$TAG" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com"
```

## Versioning policy

- Pre-1.0: `v0.MINOR.PATCH`. Backwards-incompatible API changes are
  allowed between minors and called out in release notes.
- Release candidates: `v0.1.0-rc1`, `-rc2`, … goreleaser's `prerelease: auto`
  classifies these as GitHub pre-releases automatically.
- Post-1.0: strict semver per the [Memora API
  contract](./architecture.md#api-contract).

## Known gaps (not blockers, tracked separately)

- **macOS notarization** — v0.1 darwin binaries are unsigned. First
  run on Sequoia+ triggers a Gatekeeper warning; right-click → Open
  works around it. Notarization needs an Apple Developer Program
  subscription + the `AC_USERNAME` / `AC_PASSWORD` / `AC_TEAM_ID`
  secrets. Will turn on in a follow-up.
- **Windows code signing** — unsigned `.exe` files trigger SmartScreen
  on first run. Same trade-off; needs a code-signing cert.
- **Linux .deb / .rpm packages** — not built in v0.1. Goreleaser
  supports them via the `nfpms:` section; will land once we decide
  on the apt/rpm hosting story.
