# Contributing to Memora Core

Thanks for your interest in contributing! Memora Core is an Apache-2.0
licensed OSS project under [Axiom Studio](https://axiomstudio.ai). We
welcome bug reports, feature ideas, adapter implementations, and direct
code contributions.

## Quick links

- **Code of Conduct:** see [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
- **Security disclosures:** see [SECURITY.md](SECURITY.md). Do **not** open
  public issues for security findings.
- **Bug reports & feature requests:** use the GitHub issue templates.

## Development setup

Memora Core is a standalone Go module. You need:

- Go 1.25 or newer.
- `make` (or run the Go commands directly — see the Makefile).
- Optionally `golangci-lint` v1.62+ for the lint target.

```bash
git clone https://github.com/axiom-studio/memora
cd memora
make build         # produces ./bin/memora-core and ./bin/memora-cli
make test          # go test ./... -race -cover
make vet           # go vet ./...
make lint          # golangci-lint run
```

Builds default to `CGO_ENABLED=0` so the binaries cross-compile cleanly to
every supported platform.

## Branching & PR workflow

- The default branch is `main`. All PRs target `main`.
- Branch names should describe the change: `feat/...`, `fix/...`,
  `docs/...`, `chore/...`.
- Keep PRs focused. If a change spans multiple unrelated concerns, split it.
- CI must pass before review: build, vet, test, lint, and `go mod tidy`
  exit-code check all run on every PR (see `.github/workflows/ci.yml`).

## Commit message style

Use imperative present-tense subjects ("add adapter", not "added adapter"
or "adds adapter"). Reference the work-item ID where relevant:

```
F1.T2: add Makefile, golangci-lint config, editorconfig, CI workflow

<body explaining the why, not the what>

Todo #1714 from memora
```

## **DCO sign-off (required)**

Memora Core uses the [Developer Certificate of Origin](https://developercertificate.org)
instead of a Contributor License Agreement. Every commit MUST be signed off:

```bash
git commit -s -m "..."
```

This appends a `Signed-off-by: Your Name <you@example.com>` line to the
commit message and certifies that:

1. You wrote the change or have the right to submit it.
2. You're contributing under the project's Apache-2.0 license.

PRs whose commits lack DCO sign-off will fail CI.

## Code conventions

- **No dead code.** If you write a helper, use it. If you remove a caller,
  delete the helper.
- **No mocks.** Unit tests run against real SQLite, real HTTP handlers,
  real adapters. The integration tests boot `memora-core` in-process. See
  PRD #297 §3.2 and the Adapter Compliance Test Suite (`pkg/adapter/compliance/`)
  for the testing philosophy.
- **No comments that restate the code.** Only comment the *why* — non-obvious
  invariants, decisions, gotchas. Naming carries the *what*.
- **Errors:** wrap with context. Use `fmt.Errorf("operation X on %s: %w", id, err)`.
  Never `return err` bare from a non-trivial call site.
- **Imports:** `goimports` with `local-prefixes = github.com/axiom-studio/memora`.
- **Test names:** `TestComponent_Scenario_ExpectedResult`. Names should
  read as documentation.

## Adapter contributions

Memora Core's three adapter contracts — `PrimaryStore`, `VectorStore`,
`LedgerStore` — live under `pkg/adapter/`. Third-party adapters are
welcomed under Apache-2.0.

To add an adapter:

1. Implement the relevant interface(s) in a new package under
   `internal/store/<your-driver>/` or `internal/ledger/<your-driver>/`.
2. Register the driver in `init()` via `RegisterPrimary(...)` etc.
3. Run the Adapter Compliance Test Suite (`pkg/adapter/compliance/`)
   against your implementation. All blocks (PrimaryStore / VectorStore /
   LedgerStore / Context Graph) must pass.
4. Advertise capabilities accurately via `Capabilities()`. The server
   relies on capability flags to fail fast when a request asks for
   something the adapter can't do.

See [docs/adapter-authoring.md](docs/adapter-authoring.md) (forthcoming
with F12) for a complete walkthrough.

## RFC process

Larger changes — new public verbs, schema additions, new adapter contracts,
breaking changes — should land as an RFC under `docs/rfcs/` before
implementation. An RFC is a short Markdown doc covering: motivation,
detailed design, alternatives considered, drawbacks, migration path.

For OSS v0.x, the RFC requirement is informal: open a GitHub Discussion
or Issue with `kind: rfc` and the maintainers will decide if a full RFC
is warranted.

## Maintainers

Reviews are owned by the `@axiom-studio/memora-maintainers` team — see
[CODEOWNERS](CODEOWNERS). Expect a response within 5 business days.
