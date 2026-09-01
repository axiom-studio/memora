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
docker run -p 7777:7777 -v memora-data:/data ghcr.io/axiom-studio/memora-core:latest
```

Mount a volume at `/data`, as above. Without one, the SQLite database holding every Memory, vector, edge, and ledger entry lives in the container's writable layer and is discarded when the container is removed.

A single-node development configuration ships as `docker-compose.yml` in the repository: one node on SQLite and sqlite-vec, listening on port 7777.

For Kubernetes, a Helm chart lives at `charts/memora-core`.

> **Set the chart's image repository explicitly.** The chart's default `image.repository` value does not match the repository the release pipeline publishes to, so a default `helm install` leaves the pod unable to pull. Pass `--set image.repository=ghcr.io/axiom-studio/memora-core` until the chart default is corrected.

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

Every write to a memory, edge, or graph endpoint names an agent in the `Memora-Agent-Id` header, and the named identity provider checks it before the write proceeds. An agent that passes and is not yet known is registered automatically.

| Section | Description |
|---|---|
| `opaque` | Accepts the supplied ID as given. The default, and the fallback for any unrecognised provider name. |
| `anthropic_session` | Session-based agent identity. |

The `Memora-Identity-Provider` header selects the check and defaults to `opaque`.

> **An unrecognised provider name falls back to `opaque` rather than failing.** Naming a provider the server has not registered does not return an error; the request is verified by `opaque`, which accepts any well-formed ID. A deployment that depends on a specific identity check should confirm it is actually in effect rather than assuming the header took hold.

What `opaque` provides is attribution, not authentication: it records who a write claims to be from, inside the trust boundary the API key already established. That is sufficient when every caller shares one key and the deployment trusts them equally. It is not sufficient when two mutually distrusting agents share one Memora instance.

One reserved identity is used internally. Edges created by auto-linking are attributed to `agent_system_auto_link`, which keeps system-generated edges distinguishable from anything a real agent asserted.

## Multi-Tenancy

| Field | Value |
|---|---|
| `single-tenant` | The workspace comes from the request path. The default. |
| `multi-tenant` | The `X-Memora-Workspace` header resolves the workspace for the request. |

Requests are capped at 8 MiB and each runs under the configured timeout, 30 seconds by default. Cross-origin requests receive CORS headers only for origins on the configured allow-list; with no allow-list configured, no CORS headers are sent at all.

## Observability

| Field | Value |
|---|---|
| `GET /healthz` | Liveness. Returns `200` whenever the process is up. |
| `GET /readyz` | Readiness. Pings the metadata, vector, and ledger adapters. Returns `503` with `status: degraded` and a per-adapter breakdown if any is down. |
| `GET /metrics` | Prometheus exposition. |
| `telemetry.log_level` | `debug`, `info`, `warn`, or `error`. Default `info`. |
| `telemetry.log_format` | `json` or `text`. Default `json`. |

`/readyz` reports the metadata adapter under the key `primary` rather than `metadata` — worth knowing when parsing the breakdown. The graph and content stores are not probed, so a readiness check passes even when a graph operation would return `501`.

Every request is logged with its method, path, status, duration in milliseconds, and request ID. A panic in a handler is recovered, logged, and returned as `500 internal_error` rather than dropping the connection.

> **`/metrics` is a placeholder.** It exposes a single `memora_up` gauge and nothing else — no request counts, no latency histograms, no per-adapter timings. Do not build alerting on it as it stands.

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

Console sign-in is separate from the API. Logging in exchanges the server API key for a `memora_session` cookie, set `HttpOnly` with `SameSite=Lax` and valid for 24 hours. The bearer check is skipped for `/ui` paths, so the console's authentication is the cookie alone.

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
memora-cli migrate content --workspace ws_XXXX \
    --content-driver file --content-dsn ./data/content \
    --dry-run
```

Run it in three passes:

1. **Dry run** — `--dry-run` reports the counts it would move without writing anything. Use it to confirm the workspace and target are right.
2. **Migrate** — drop `--dry-run`. `--max-rate` limits the rate in Memories per second, defaulting to `100`; set `0` to remove the limit. If the run is interrupted, `--resume-from <mem_id>` continues from that Memory rather than starting over.
3. **Verify** — `--verify` compares content between the metadata store and the content store. The command prints a JSON result and exits `4` when any mismatch is found, which makes this pass usable as a deployment gate.

Only after a clean verification should `storage.content_driver` be pointed at the new store in the server's configuration. See [Configuration](configuration.md) for the keys, and [CLI Reference](cli.md) for the full flag list.
