# Configuration

Memora is configured from three layers — a TOML file, environment variables, and command-line flags — resolved in that order. This page lists every key each layer accepts, its type, and its default.

## How Configuration Resolves

Later sources override earlier ones, so a flag beats an environment variable, which beats the file, which beats the built-in defaults.

```text
built-in defaults → TOML file → environment variables → command-line flags
```

The file is found by taking the first path that exists from this list:

```text
$MEMORA_CONFIG → --config → ~/.memora/config.toml → ./memora.toml → (defaults only)
```

Paths inside the file that begin with `~/` are expanded against the current user's home directory. When no file is found, the built-in defaults apply and nothing is reported as missing.

Two subcommands exist to inspect the result rather than infer it:

```bash
memora-core print-config     # render the resolved config, secrets replaced with ***
memora-core check-config     # validate it; prints "config ok" or exits 78
```

Both accept `--config` and both apply the environment layer, so they show what the server would see. `check-config` exits `78` on any validation error, which makes it usable as a deployment gate.

## Server Keys

TOML table `[server]`.

| Key | Type | Default |
|---|---|---|
| `server.addr` | string | `:7777` |
| `server.mode` | string | `single-tenant` |
| `server.api_key` | string | *(empty)* |
| `server.api_key_file` | string | *(empty)* |
| `server.allow_no_auth` | bool | `false` |
| `server.mcp_enable` | bool | `false` |
| `server.ui_cookie_secure` | string | *(empty — `always` on a non-loopback `addr`, `auto` on loopback)* |

`server.mode` accepts `single-tenant` or `multi-tenant`; any other value fails validation. In single-tenant mode the workspace comes from the request path. In multi-tenant mode the `X-Memora-Workspace` header resolves it per request.

`server.api_key` and `server.api_key_file` are mutually exclusive, and setting both fails validation. The file form is the safer one for deployments that mount secrets, and Memora refuses to read a key file that is world-readable, reporting the offending mode — so the file must be `0600` or `0640`.

`server.mcp_enable` mounts an MCP WebSocket endpoint at `/mcp` on the HTTP server, in addition to the stdio transport that `memora-core mcp` provides.

`server.ui_cookie_secure` sets whether the console's session cookie is marked `Secure`: `always`, `never`, or `auto`, which marks it `Secure` only when the request itself arrived over TLS. Behind a TLS-terminating reverse proxy or Ingress the server only ever sees plain HTTP, so `auto` would never mark it `Secure` there; that is why the default on a non-loopback address is `always`. A `Secure` cookie is named `__Host-memora_session` and the console also sends `Strict-Transport-Security`. If users reach the console over plain HTTP on a non-loopback address, browsers drop a `Secure` cookie and login loops back to the login page; set `auto` for that setup, and the server logs a warning the first time it happens.

> **`allow_no_auth` is an override, not a convenience.** With no API key configured, the server starts on a loopback bind and refuses to start on any other address. Setting `allow_no_auth` suppresses that refusal, leaving the API unauthenticated on a reachable address. See [Operations](operations.md) before using it.

## TLS Keys

TOML table `[server.tls]`.

| Key | Type | Default |
|---|---|---|
| `server.tls.enabled` | bool | `false` |
| `server.tls.cert_file` | string | *(empty)* |
| `server.tls.key_file` | string | *(empty)* |
| `server.tls.auto_self_signed` | bool | `false` |

Enabling TLS requires either a `cert_file` and `key_file` pair or `auto_self_signed`; without one of those, validation fails. When the key file's permissions allow group or world access, the server logs a warning naming the path and mode but still starts.

TLS is file-only. There is no environment variable or `serve` flag for any of these keys.

## Storage Keys

TOML table `[storage]`. Each seam takes a driver name and an optional DSN.

| Key | Type | Default |
|---|---|---|
| `storage.data_dir` | string | `./data` |
| `storage.metadata_driver` | string | `sqlite` |
| `storage.metadata_dsn` | string | *(empty — falls back to `<data_dir>/memora.db`)* |
| `storage.vector_driver` | string | `sqlite-vec` |
| `storage.vector_dsn` | string | *(empty — falls back to `<data_dir>/memora.db`)* |
| `storage.graph_driver` | string | `sqlite_graph` *(set `"none"` to disable the graph)* |
| `storage.graph_dsn` | string | *(empty — falls back to the metadata DSN)* |
| `storage.ledger_driver` | string | `sqlite` |
| `storage.ledger_dsn` | string | *(empty — falls back to `<data_dir>/memora.db`)* |
| `storage.content_driver` | string | *(empty — content store disabled)* |
| `storage.content_dsn` | string | *(empty — see below)* |

At the defaults, four of the five seams share one SQLite file at `<data_dir>/memora.db` and the content store is switched off entirely, which is what makes a fresh instance dependency-free.

`sqlite_graph` stores edges in the SQLite metadata database, so it requires `metadata_driver = "sqlite"`; `check-config` rejects any other combination. With a non-SQLite metadata store, set `graph_driver = "none"` (or `MEMORA_GRAPH_DRIVER=none`) to run without the context graph; an empty string also disables it, but cannot be set through the environment. With the graph disabled, graph operations report that the graph store is not configured.

The content store is the one seam that is off rather than defaulted. When `content_driver` is set and `content_dsn` is not, the DSN becomes the SQLite database for the `sqlite` driver and `<data_dir>/content` for any other driver. See [Adapters](adapters.md) for which drivers each seam accepts.

## Embedding Keys

TOML table `[embedding]`.

| Key | Type | Default |
|---|---|---|
| `embedding.model` | string | `noop:default` |
| `embedding.async` | bool | `false` |

The value is a `prefix:model` string where the prefix selects the provider. The default provider is a stub that returns meaningless vectors; set a real one before evaluating recall quality.

By default a write embeds its changed Cells before it returns, so an imprint responds with `recall_ready: true`. With `embedding.async = true` the write returns as soon as the Cells are stored and a background worker pool embeds them: imprint responds with `recall_ready: false`, and the Memory's `recall_ready` turns `true` once all of its Cells have vectors. Poll a lookup to see when it does. The re-embedded and skipped Cell counts in write responses and the ledger are the same in both modes. On shutdown the server finishes embedding the queued Cells before closing its stores.

## Node Keys

TOML table `[node]`. These identify this node to federation peers and in search results.

| Key | Type | Default |
|---|---|---|
| `node.id` | string (UUIDv4) | *(minted on first start)* |
| `node.name` | string | the hostname |

When `node.id` is unset, the node mints a UUIDv4 the first time it starts and stores it in the metadata store. It keeps that ID across restarts, including in containers without a config file. `memora-core node id` prints it. Set `node.id` only to pin a specific ID.

Federation peers are listed under `[[federation.peers]]`. Each peer needs `node_id`, which is the peer's own node ID (run `memora-core node id` on the peer), plus `name` and `url`. `federation.federation_id` is optional; if set, it is a UUIDv4 label for the cluster.

### Peer Credentials

Each direction between two nodes has its own key. The routes a peer calls, `POST /v1/federation/search` and `GET /v1/federation/memories/{memory_id}`, take that peer's key instead of the server API key. All other routes, including the peer-management routes under `/v1/federation/peers`, keep the API key.

| Key | Type | Default |
|---|---|---|
| `api_key` | string | *(empty)* |
| `inbound_key_sha256` | string (64 hex characters) | *(empty)* |
| `client_cert_sha256` | string (64 hex characters) | *(empty)* |
| `revoked` | bool | `false` |
| `tls_cert` | string (path) | *(empty)* |
| `client_cert_file`, `client_key_file` | string (path) | *(empty)* |

- **`api_key`** is the key this node sends when it calls the peer. It goes in `X-Memora-Federation-Auth: Bearer <key>`, and the node sends its node ID in `X-Memora-Local-Instance-ID`.
- **`inbound_key_sha256`** is the SHA-256 of the key the peer sends when it calls this node. Only the hash is configured, so the key never sits in this node's config, logs or responses. The comparison runs in constant time. A peer without it cannot call this node, and the server logs a warning at startup.
- **`client_cert_sha256`**, when set, also requires the peer to present a TLS client certificate with this SHA-256 fingerprint. It needs `server.tls`; the server refuses to start without it.
- **`revoked = true`** keeps the peer listed but refuses its calls.
- **`tls_cert`** is the CA certificate used to verify the peer's server certificate on outbound calls.
- **`client_cert_file`** and **`client_key_file`** are the TLS client certificate this node presents when it calls the peer. Set both or neither. They pair with the peer's `client_cert_sha256` for this node. A file that cannot be loaded stops the server at startup.

`memora-core node peer-key` prints a new random key and its SHA-256. Put the key in the calling node's `api_key` for this peer, and the hash in this node's `inbound_key_sha256` for the caller.

| Status | Error code | Cause |
|---|---|---|
| `401` | `federation_unauthorized` | Unknown node ID, missing or wrong key, or missing or wrong client certificate. These causes are not distinguished. |
| `403` | `federation_peer_revoked` | The peer presented a valid key, but `revoked` is set. |
| `404` | `federation_disabled` | Federation is not enabled on this node. |
| `508` | `federation_loop` | This node is already in the request's federation path. |

Peer routes need peer credentials even when the server runs without an API key.

## Telemetry Keys

TOML table `[telemetry]`.

| Key | Type | Default |
|---|---|---|
| `telemetry.log_level` | string | `info` |
| `telemetry.log_format` | string | `json` |

`log_level` accepts `debug`, `info`, `warn`, or `error`. `log_format` accepts `json` or `text`. An empty value is treated as the default; any other value fails validation. Both are file-only.

## Environment Variables

Each variable overrides its file counterpart when set and non-empty.

| Key | Type | Default |
|---|---|---|
| `MEMORA_CONFIG` | string | *(unset — see the search path above)* |
| `MEMORA_ADDR` | string | `:7777` |
| `MEMORA_NODE_ID` | string | *(minted on first start)* |
| `MEMORA_NODE_NAME` | string | the hostname |
| `MEMORA_MODE` | string | `single-tenant` |
| `MEMORA_API_KEY` | string | *(empty)* |
| `MEMORA_DATA_DIR` | string | `./data` |
| `MEMORA_METADATA_DRIVER` | string | `sqlite` |
| `MEMORA_METADATA_DSN` | string | *(empty)* |
| `MEMORA_VECTOR_DRIVER` | string | `sqlite-vec` |
| `MEMORA_VECTOR_DSN` | string | *(empty)* |
| `MEMORA_GRAPH_DRIVER` | string | `sqlite_graph` |
| `MEMORA_GRAPH_DSN` | string | *(empty)* |
| `MEMORA_LEDGER_DRIVER` | string | `sqlite` |
| `MEMORA_LEDGER_DSN` | string | *(empty)* |
| `MEMORA_CONTENT_DRIVER` | string | *(empty)* |
| `MEMORA_CONTENT_DSN` | string | *(empty)* |
| `MEMORA_EMBEDDING_MODEL` | string | `noop:default` |
| `MEMORA_EMBEDDING_ASYNC` | bool | `false` — set to the literal `true` to enable |
| `MEMORA_MCP_ENABLE` | bool | `false` — set to the literal `true` to enable |
| `MEMORA_UI_COOKIE_SECURE` | string | *(empty)* — `auto`, `always` or `never` |

`MEMORA_EMBEDDING_ASYNC` and `MEMORA_MCP_ENABLE` are the only booleans in this layer, and each is matched against the exact string `true`; any other value leaves the file setting alone. There are no environment variables for the TLS keys, the API key file, `allow_no_auth`, or telemetry.

## Flags for `serve`

| Key | Type | Default |
|---|---|---|
| `--config` | string | *(empty — uses the search path)* |
| `--addr` | string | *(empty — falls through to file or env)* |
| `--data-dir` | string | *(empty)* |
| `--metadata-driver` | string | *(empty)* |
| `--vector-driver` | string | *(empty)* |
| `--graph-driver` | string | *(empty)* |
| `--ledger-driver` | string | *(empty)* |
| `--content-driver` | string | *(empty)* |
| `--content-dsn` | string | *(empty)* |
| `--embedding-model` | string | *(empty)* |
| `--async-embed` | bool | `false` |
| `--api-key` | string | *(empty)* |
| `--allow-no-auth` | bool | `false` |
| `--mode` | string | *(empty)* |
| `--mcp-enable` | bool | `false` |

An empty flag value means "do not override", so omitting a flag leaves whatever the environment or file supplied. Note that `--content-dsn` is the only DSN available as a flag; the metadata, vector, graph, and ledger DSNs can only be set through the file or their environment variables.

## Flags for `mcp`

The stdio MCP server takes a smaller set, because it has no listen address and no HTTP surface.

| Key | Type | Default |
|---|---|---|
| `--config` | string | *(empty — uses the search path)* |
| `--data-dir` | string | *(empty)* |
| `--metadata-driver` | string | *(empty)* |
| `--vector-driver` | string | *(empty)* |
| `--ledger-driver` | string | *(empty)* |
| `--embedding-model` | string | *(empty)* |
| `--api-key` | string | *(empty)* |

There is no `--graph-driver` or `--content-driver` here; both seams take their configuration from the file or the environment when running under `mcp`.

## A Worked Example

A configuration moving metadata, vectors and the ledger to Postgres, running without the context graph, enabling a file content store, and reading the API key from disk:

```toml
[server]
addr = ":7777"
mode = "single-tenant"
api_key_file = "~/.memora/api.key"

[server.tls]
enabled = true
cert_file = "/etc/memora/tls/cert.pem"
key_file = "/etc/memora/tls/key.pem"

[storage]
data_dir = "/var/lib/memora"
metadata_driver = "postgres"
metadata_dsn = "postgres://memora@db:5432/memora?sslmode=require"
vector_driver = "pgvector"
vector_dsn = "postgres://memora@db:5432/memora?sslmode=require"
graph_driver = "none"
ledger_driver = "postgres"
ledger_dsn = "postgres://memora@db:5432/memora?sslmode=require"
content_driver = "file"
content_dsn = "/var/lib/memora/content"

[embedding]
model = "openai:text-embedding-3-small"

[telemetry]
log_level = "info"
log_format = "json"
```

`graph_driver` is `"none"` because `sqlite_graph`, the only registered graph driver, keeps its edges in the SQLite metadata database and so needs `metadata_driver = "sqlite"`. On Postgres metadata the context graph is therefore unavailable: graph operations report that the graph store is not configured and imprints are not auto-linked. Validate the file before deploying it:

```bash
memora-core check-config --config /etc/memora/config.toml
```
