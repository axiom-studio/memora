# Operations

This page covers running Memora as a service: how it is built and deployed, how requests are authenticated, how agent identity is checked, what can be observed, and how content is migrated between stores.

## Building and Testing

| Section | Description |
|---|---|
| `make build` | Build `bin/memora-core` and `bin/memora-cli`. |
| `make test` | Run the unit tests. |
| `make cover` | Run the tests with coverage. |
| `make lint` | Run `golangci-lint`. |
| `make vet` | Run `go vet`. |
| `make tidy` | Run `go mod tidy`. |
| `make clean` | Remove build output. |

Both binaries are built with `CGO_ENABLED=0`, so they are static and carry no shared-library dependencies.

## Container Deployment

The published image is built on a distroless base carrying only the two binaries. It exposes port 7777, runs as a non-root user, and defaults to the `serve` subcommand with `MEMORA_DATA_DIR` set to `/data`.

```bash
docker run -p 7777:7777 -v memora-data:/data \
    -e MEMORA_API_KEY=dev-key-change-me \
    ghcr.io/axiom-studio/memora-core:latest
```

The API key is required, not optional. The image binds `:7777`, which is reachable beyond loopback, and the server refuses to start unauthenticated on such an address — so a `docker run` without `MEMORA_API_KEY` exits immediately.

Mount a volume at `/data`, as above. Without one, the SQLite database holding every Memory, vector, edge, and ledger entry lives in the container's writable layer and is discarded when the container is removed.

A single-node development configuration ships as `docker-compose.yml` in the repository: one node on SQLite and sqlite-vec, published on `127.0.0.1:7777` only. It reads `MEMORA_API_KEY` from a `.env` file next to it and will not start without one; copy `.env.example` to `.env` and set a key first.

## Authentication

Memora has two independent authentication paths: a bearer token for the API, and a session cookie for the web console.

| Field | Value |
|---|---|
| `Authorization: Bearer <key>` | The server API key. Compared in constant time. Required on every non-`/ui` path when a key is configured. |
| `server.api_key` | The key itself, set in the config file or as `MEMORA_API_KEY`. |
| `server.api_key_file` | A path to read the key from instead. Mutually exclusive with `server.api_key`. |

The key file must not be world-readable. Memora refuses to read one that is and reports the offending mode, so the file needs to be `0600` or `0640`.

### Running Without a Key

With no API key configured, the bearer check is skipped entirely — there is no implicit deny. The server guards against that becoming an accident:

```text
no API key + loopback bind        → starts, unauthenticated, logs a warning
no API key + any other bind       → refuses to start
no API key + --allow-no-auth      → starts, unauthenticated, on any bind
```

An unauthenticated server on a loopback address is the intended local-development shape. On any other address the server exits rather than serving, and `--allow-no-auth` (or `server.allow_no_auth`) is the deliberate override. Treat that flag as a statement that something else — a private network, a reverse proxy, a service mesh — is doing the authenticating.

When a key is configured, the server logs an 8-character hash of it at startup. That is enough to confirm which key is loaded across a restart or a rollout without putting the key itself in the logs.

### TLS

The server speaks plain HTTP unless `server.tls.enabled` is set, in which case it needs either a certificate and key pair or `auto_self_signed`. To generate a self-signed certificate:

```bash
memora-core init-cert --host memora.internal --san memora.local --output ./tls
```

It writes an ECDSA P-256 key pair by default, or RSA 4096 with `--rsa`, valid for 365 days, defaulting to `~/.memora/tls/`. `--san` is repeatable. The command prints the certificate path, the key path, a SHA-256 fingerprint, and a ready-to-paste config snippet.

The server warns at startup when the private key's permissions allow group or world access, and again when a self-signed certificate is serving a non-loopback address. Both are warnings, not refusals — a self-signed certificate on an internal address is a legitimate configuration, and Memora does not try to guess which case it is in.

## Agent Identity

Every write to a memory, edge, or graph endpoint names an agent in the `Memora-Agent-Id` header, and the named identity provider checks it before the write proceeds. Agent IDs are UUIDv4s like every other ID; anything else is rejected with `400 invalid_input`. Human-readable names belong in the agent's `display_name`. An agent that passes and is not yet known is registered automatically.

| Section | Description |
|---|---|
| `opaque` | Accepts the supplied ID as given. The default, and the fallback for any unrecognised provider name. |
| `anthropic_session` | Session-based agent identity, over MCP. The client proves its session in `initialize`; Memora maps that session to a UUIDv4 agent ID, minted on first use, and returns it. `oauth_agent` and `oidc_agent` work the same way, keyed on the token's identifying claim. |

The `Memora-Identity-Provider` header selects the check and defaults to `opaque`.

What `opaque` provides is attribution, not authentication: it records who a write claims to be from, inside the trust boundary the API key already established. That is sufficient when every caller shares one key and the deployment trusts them equally. It is not sufficient when two mutually distrusting agents share one Memora instance.

Two reserved identities are used internally. Edges created by auto-linking are attributed to the fixed agent ID `2a7e5d1c-8f3b-4c6a-9e0d-4b1f2c3a5d61` (display name `system-auto-link`), which keeps system-generated edges distinguishable from anything a real agent asserted. Writes made through the web console are attributed to `8d3b6e1f-4a2c-4f5d-b7e9-0c1a2b3d4e5f` (display name `ui-admin`).

## Multi-Tenancy

| Field | Value |
|---|---|
| `single-tenant` | The workspace comes from the request path. The default. |
| `multi-tenant` | The `X-Memora-Workspace` header resolves the workspace for the request. |

> **Multi-tenant mode is not an isolation boundary.** The workspace comes from a header the client chooses, and every client holds the same API key, which can read every workspace through the workspace routes. The mode scopes requests for cooperating clients. Node-wide search fails closed: `/v1/search` without the header returns `400 missing_workspace`, and MCP `memora_search` requires `workspace_ids`. Isolating tenants that do not trust each other needs per-tenant credentials, which Memora does not provide.

Requests are capped at 8 MiB and each runs under the configured timeout, 30 seconds by default. Cross-origin requests receive CORS headers only for origins on the configured allow-list; with no allow-list configured, no CORS headers are sent at all.

## Observability

| Field | Value |
|---|---|
| `GET /healthz` | Liveness. Returns `200` whenever the process is up. |
| `GET /readyz` | Readiness. Pings the metadata, vector, and ledger adapters. Returns `503` with `status: degraded` and a per-adapter breakdown if any is down. |
| `telemetry.log_level` | `debug`, `info`, `warn`, or `error`. Default `info`. |
| `telemetry.log_format` | `json` or `text`. Default `json`. |

Both endpoints require the bearer token when an API key is configured — they are ordinary non-`/ui` paths, not auth exemptions. A health probe that does not send the key receives `401`, so configure probes with the `Authorization` header rather than as anonymous requests.

`/readyz` reports the metadata adapter under the key `primary` rather than `metadata` — worth knowing when parsing the breakdown. The graph and content stores are not probed, so a readiness check passes even when a graph operation would return `501`.

Every request is logged with its method, path, status, duration in milliseconds, and request ID. A panic in a handler is recovered, logged, and returned as `500 internal_error` rather than dropping the connection.

### The Ledger as Telemetry

The real per-operation record is the audit ledger. Every mutation appends an entry carrying the operation, its target, the acting agent, the before and after watermarks, a request ID, elapsed milliseconds, and operation-specific counters — Cells created, re-embedded, skipped, added, and removed, patches applied, edges auto-linked, and edges cascaded on delete.

Querying the ledger over a time range therefore yields write throughput, latency distribution, and re-embedding efficiency directly, without a separate metrics pipeline:

```bash
curl -H "Authorization: Bearer $MEMORA_API_KEY" \
    "$MEMORA_ENDPOINT/v1/workspaces/$MEMORA_WORKSPACE/ledger?op=patch,imprint&since=2026-01-01T00:00:00Z"
```

The endpoint filters by `since`, `until`, `actor`, `agent_id`, `memory_id`, `edge_id`, and `op`, and pages with `since_ledger_id`. The ledger is reachable over REST and through the Go SDK's `LedgerQuery`; `memora-cli` has no ledger command, so scripted audit extraction goes through one of those two.

> **Not every ledger driver can be queried.** Query and redaction are optional capabilities. A ledger that does not support querying still records every entry, but the audit endpoint returns `501 capability_unavailable`.

### Deleting Under Audit

`DELETE` on a Memory with `?redact_audit=true` also redacts the `metadata`, `ip`, and `user_agent` fields from that Memory's ledger entries, where the configured ledger supports both querying and redaction. The entries themselves remain, and the log stays append-only — redaction removes the personal data from a record without removing the record that the operation happened.

## The Web Console

`memora-core` serves a console at `/ui`: server-rendered Go templates with htmx for partial updates, so there is no build step and no JavaScript bundle to deploy. Seven pages ship — Home, Workspaces, Memories, Audit, Settings, Login, and a graph view reached from a workspace.

The console covers workspace and collection management, imprint and file upload, patch and append, recall with a saved-query bar, graph link and unlink with a neighbours and traversal browser, agent registration and deactivation, the bulk operations below, and audit browsing with export.

Console sign-in is separate from the API. Logging in exchanges the server API key for a session cookie, set `HttpOnly` with `SameSite=Lax` and valid for 24 hours. The bearer check is skipped for `/ui` paths, so the console's authentication is the cookie alone.

On a non-loopback address the cookie is `Secure` by default: it is named `__Host-memora_session`, and the console sends `Strict-Transport-Security`. This is what keeps the session safe behind a TLS-terminating proxy or Ingress, where the server itself only sees plain HTTP. On loopback, or with `server.ui_cookie_secure = "auto"`, the cookie is `memora_session` and is `Secure` only on a direct TLS connection. The Helm chart sets `always` when its Ingress has TLS and `auto` otherwise (`uiCookieSecure` overrides it). See [Configuration](configuration.md).

> **The console's session is as strong as the API key.** Anyone who can sign in to the console holds the API key, because the key is the login credential. There are no per-user console accounts and no roles.

## Bulk Operations

The console provides four bulk operations against a workspace, each streaming progress as it runs.

| Section | Description |
|---|---|
| Seed | Populate a workspace with generated sample data. |
| Dump | Export a workspace's memories. |
| Import | Load memories from an uploaded file. |
| Replay | Re-apply a set of recorded operation records. |

These are console endpoints under `/ui/api/bulk/`, not part of the `/v1` API, and uploads are size-capped. They are built for an operator working interactively; anything scripted should use the CLI or the Go SDK instead, both of which are stable surfaces in a way the console's internal endpoints are not.

## Content Migration

Schema changes ship as versioned SQL files applied by each store. Moving Memory and Cell bodies out of the metadata store and into a dedicated content store is a separate, explicit backfill rather than part of a schema migration, because it moves data between two systems that can fail independently.

The command opens the metadata and content stores directly rather than going through the API, so it runs on a host with access to the data directory:

```bash
memora-cli migrate content --workspace 3f2b8c1e-6d4a-4f0b-9a7e-2c5d8e1f4a90 \
    --content-driver file --content-dsn ./data/content \
    --dry-run
```

Run it in three passes:

1. **Dry run** — `--dry-run` reports the counts it would move without writing anything. Use it to confirm the workspace and target are right.
2. **Migrate** — drop `--dry-run`. `--max-rate` limits the rate in Memories per second, defaulting to `100`; set `0` to remove the limit. If the run is interrupted, `--resume-from <mem_id>` continues from that Memory rather than starting over.
3. **Verify** — `--verify` compares content between the metadata store and the content store. The command prints a JSON result and exits `4` when any mismatch is found, which makes this pass usable as a deployment gate.

Only after a clean verification should `storage.content_driver` be pointed at the new store in the server's configuration. See [Configuration](configuration.md) for the keys, and [CLI Reference](cli.md) for the full flag list.

## Changing the Embedding Model

Every vector records the model and dimension that produced it, and vector search compares a query only with vectors from the active model. After `embedding.model` changes, the old vectors are kept but not searched. Vector recall returns nothing for memories not yet re-embedded, and keyword search is unaffected. `GET /readyz` reports the count as `stale_vectors`, and the server logs it at start-up.

Re-embed with the new model:

```bash
memora-core reembed --config memora.toml --dry-run   # count the cells to re-embed
memora-core reembed --config memora.toml             # re-embed them
```

| Flag | Value |
|---|---|
| `--workspace` | Re-embed one workspace instead of all of them. |
| `--dry-run` | Count only; write nothing. |
| `--batch` | Memories per batch. Default `100`. |

It re-embeds only cells whose vector is missing or from another model or dimension, and it writes each batch as it goes. An interrupted run picks up where it stopped when started again, and a run with nothing to do is a no-op. It prints the counts as JSON.

With `pgvector`, the vector column has a fixed width. If the new model's dimension differs, the server refuses to start and names `memora-core reembed`. A real `reembed` run drops the old vectors, which the new model cannot use, resizes the column and re-embeds every cell. A `--dry-run` reports the mismatch and changes nothing. Plan for vector search to be empty until the run finishes, or switch back to the old model to undo.

## Upgrading from Prefixed IDs

Memora identifiers are UUIDv4s, such as `3f2b8c1e-6d4a-4f0b-9a7e-2c5d8e1f4a90`. Builds before that change used prefixed ULIDs (`ws_01H…`, `mem_01H…`). Those IDs appear in every table, as vector-store keys and in content-store file paths.

No tagged release has shipped with prefixed IDs, so pre-1.0 deployments are not migrated: **start the new version with an empty database**. That means a new `storage.data_dir` for SQLite, or new databases or schemas for Postgres and the content store.

The server checks for this. If the metadata store contains a workspace with a prefixed ID, `memora-core serve` and `memora-core mcp` exit at start-up with an error naming that workspace. They do not serve data that every request would reject.

To keep memories, read their content from the old deployment (the console's Bulk → Dump, or `memora-cli lookup`) and imprint it into the new one. The memories get new IDs. Watermark history, edges and the ledger do not carry over.
