# API Reference

Memora exposes the same service layer through three programmable surfaces: a REST API under `/v1`, an MCP server for agent clients, and a Go SDK. This page lists the endpoints, tools, and methods of each, plus the error codes common to all of them.

## Request Conventions

Request and response bodies are JSON. Errors use one envelope shape across every endpoint:

```json
{
  "error_code": "cas_conflict",
  "message": "expected watermark b2c9e4f1-… but head is e6a1d3b8-…",
  "details": {}
}
```

Unknown fields in a request body are rejected rather than ignored, so a misspelled key surfaces as `400 invalid_input` instead of being silently dropped.

Every identifier is a lowercase UUIDv4: path segments, `*_id` and `*_ids` body fields, recall and search filters (`collection_id`, `agent_id`, `agent_id_in`, `agent_id_not_in`), `memory_ids`, `peers`, and the ledger's ID query parameters. Anything else, such as a legacy prefixed ID, an uppercase UUID or free text, is `400 invalid_input` with a message naming the field (`memory_id must be a UUID`). A well-formed UUID that matches nothing is `404`. A malformed ID in `POST /workspaces/{ws}/edges:batch` fails the whole batch with `400` instead of being reported per edge. A `/v1` path that is not an endpoint is `404 not_found`, and a method an endpoint does not support is `405 method_not_allowed` with an `Allow` header.

### Headers

| Field | Value |
|---|---|
| `Authorization: Bearer <key>` | The server API key, compared in constant time. Required on every non-`/ui` path when a key is configured. |
| `Memora-Agent-Id` | The writing agent. Required on writes to memories, edges, and graph endpoints. |
| `Memora-Identity-Provider` | Selects the identity check for that agent. Defaults to `opaque`. |
| `X-Memora-Workspace` | Resolves the workspace when the server runs in `multi-tenant` mode. |
| `If-Match` | The expected watermark on update, patch, and append. Equivalent to `expected_watermark` in the body. |
| `X-Request-ID` | Correlation ID. Echoed back when supplied as 1–128 characters of `A-Z a-z 0-9 . _ : -`; otherwise (missing or any other value) the server generates a UUIDv4 and returns it. |

The agent header is required on non-`GET` requests whose path contains `/memories`, `/edges`, or `/graph/`. Workspace, collection, agent, recall, and pin requests do not require it. A write without the header returns `400 missing_agent_id`; one whose identity provider rejects the agent returns `401 agent_verification_failed`.

Every request is capped at 8 MiB and runs under the server's configured timeout, 30 seconds by default. Cross-origin requests are answered only for origins on the configured allow-list.

## REST Endpoints

All paths are relative to `/v1`.

### Workspaces and Collections

| Field | Value |
|---|---|
| `POST /workspaces` | Create a workspace. |
| `GET /workspaces` | List workspaces, newest first. `limit` defaults to 100 and is capped at 200. Pass the response's `next_cursor` as `cursor` for the next page; the last page has no `next_cursor`. |
| `GET /workspaces/{ws}` | Read a workspace. |
| `PUT /workspaces/{ws}` | Replace a workspace. |
| `DELETE /workspaces/{ws}` | Delete a workspace. |
| `GET /workspaces/{ws}/collections` | List collections. |
| `POST /workspaces/{ws}/collections` | Create a collection. |
| `GET /workspaces/{ws}/collections/{id}` | Read a collection. |
| `DELETE /workspaces/{ws}/collections/{id}` | Delete a collection. |

### Memories

| Field | Value |
|---|---|
| `POST /workspaces/{ws}/memories` | Imprint. Accepts an optional per-request chunker and chunker config. |
| `GET /workspaces/{ws}/memories` | List memories newest first, optionally filtered by `collection_id`. `limit` defaults to 50 and is capped at 1000. Pages with `cursor` and `next_cursor` like `GET /workspaces`. |
| `GET /workspaces/{ws}/memories/{id}` | Look up a Memory with its Cells. |
| `PUT /workspaces/{ws}/memories/{id}` | Replace content. Requires a watermark. |
| `PATCH /workspaces/{ws}/memories/{id}` | Apply diff operations. Requires a watermark. |
| `DELETE /workspaces/{ws}/memories/{id}` | Forget. `?redact_audit=true` also redacts ledger metadata where the ledger supports it. |
| `POST /workspaces/{ws}/memories/{id}:append` | Append to content. |
| `GET /workspaces/{ws}/memories/{id}@{watermark}` | Read the Memory as it stood at a past watermark. |
| `GET /workspaces/{ws}/memories/{id}/watermarks` | Watermark history over a fixed seven-day window. |
| `PUT /workspaces/{ws}/memories/{id}/tags/{key}` | Upsert a tag. `POST` behaves identically. |
| `DELETE /workspaces/{ws}/memories/{id}/tags/{key}` | Delete a tag. |

### Graph

| Field | Value |
|---|---|
| `GET /workspaces/{ws}/memories/{id}/edges` | List a Memory's edges, filtered by `direction` and `type`. |
| `POST /workspaces/{ws}/memories/{id}/edges` | Create an edge from this Memory. |
| `POST /workspaces/{ws}/edges/:batch` | Create many edges in one call. Returns per-edge status with `ok` and `failed` counts. |
| `DELETE /workspaces/{ws}/edges/{id}` | Unlink. Rejects synthetic edges. |
| `POST /workspaces/{ws}/graph/neighbors` | One-hop neighbours with direction, type, and `k`. |
| `POST /workspaces/{ws}/graph/traverse` | Breadth-first traversal returning layers. |
| `GET /workspaces/{ws}/graph/stats` | Node count and edge count by type. |

> **Note the slash in `/edges/:batch`.** Batch edge creation is addressed as its own path segment — `/edges/:batch`. Written without the slash, as `/edges:batch`, the request returns `404 not_found`.

### Recall, Agents, and Audit

| Field | Value |
|---|---|
| `POST /search` | Search every workspace on the node, or those in `workspace_ids`. Body: `query`, `mode` (`keyword`, `vector`, or `hybrid`, the default), `k` (default 5, at most 100; more is `400 invalid_input`), `workspace_ids`, `filters`, `include_content`, `max_content_bytes` (default 64 KiB, at most 1 MiB), `include_cells` (at most 256 Cells per hit). Each hit carries its `workspace_id`, `matched_text` (the full text of the matching Cell, located by `cell_id` and `cell_index`), a short `text` excerpt, `tags`, `written_by_agent_id` and `created_at`. With `include_content` it also carries the Memory's `content`, with `content_truncated` set if it was cut. One response carries at most 8 MiB of content and Cell text across all its hits: in rank order, the hit that reaches the limit has its content cut and its remaining Cells left out (`content_truncated`, `cells_truncated`), and later hits keep only `text` and `matched_text`. Recall accepts the same three options and limits; its lookup mode takes at most 100 `memory_ids`. In `multi-tenant` mode the `X-Memora-Workspace` header is required (`400 missing_workspace` without it) and the search stays inside that workspace. With `federated: true` the node also searches its federation peers; see [Federated Search](#federated-search). |
| `POST /workspaces/{ws}/recall` | Run a recall query. |
| `GET /workspaces/{ws}/recall/pins` | List saved queries. |
| `POST /workspaces/{ws}/recall/pins` | Save a query. `mode` defaults to `hybrid` and `k` to 10. |
| `DELETE /workspaces/{ws}/recall/pins/{id}` | Delete a saved query. |
| `GET /workspaces/{ws}/agents` | List agents. |
| `POST /workspaces/{ws}/agents` | Register an agent. |
| `GET /workspaces/{ws}/agents/{id}` | Read an agent. |
| `DELETE /workspaces/{ws}/agents/{id}` | Deactivate an agent. Its writes in that workspace (REST, MCP and console) are then refused with `403 agent_deactivated` (MCP `-32001`); other workspaces are unaffected. A write never reactivates it: registering it again with `POST /workspaces/{ws}/agents` does. Both are written to the ledger (`agent_deactivate`, `agent_register` with `reactivated`). |
| `GET /workspaces/{ws}/ledger` | Query the audit ledger. Returns `501` if the configured ledger cannot query. |

Listings are ordered by creation time, newest first, with the ID breaking ties, so pages stay stable while new records arrive: a new record appears on the first page and never shifts later pages. Cursors are opaque; a malformed one returns `400 invalid_input`. The SDK's `ListWorkspacesPage` and `ListMemoriesPage` return the next cursor; `ListWorkspaces` and `ListMemories` return the first page. The MCP list tools return the first page only.

Ledger queries accept `since`, `until`, `actor`, `agent_id`, `memory_id`, `edge_id`, `op` (comma-separated), `limit`, and `since_ledger_id` for cursor paging.

### Federated Search

`POST /search` with `federated: true` searches this node and, in parallel, each of its peers' `POST /federation/search`. Each peer embeds the query with its own model.

| Field | Value |
|---|---|
| `federated` | `true` to include peers. |
| `peers` | Node IDs to ask. Default: every peer that is not `revoked` and has an `api_key`. An unknown or unusable node ID is `400`. |
| `per_peer_k` | How many hits each peer returns. Default `k`, at most 100. |
| `timeout_ms` | Bounds every peer. Default: each peer's `timeout_ms` (see [Peer Credentials](configuration.md#peer-credentials)). At most `60000`. |
| `rerank` | `true` passes the merged hits through the node's re-scorer before the top `k` is taken. The default re-scorer keeps the fused order; embedders of Memora can plug in their own (`federation.Reranker`). |

This node's list and each peer's list are merged by reciprocal rank fusion: ranks, not scores, are combined, because the nodes run different embedding models. A hit's `score` is its fused score. Hits are identified by `(node_id, memory_id)`, so the same memory ID on two nodes is two hits. Every hit carries `node_id` and `node_name`; a peer's hits have `via: "federation:<node_id>"`, with the node taken from this node's peer config rather than from the peer's answer.

A peer that fails, times out or is over this node's outbound limit for it does not fail the search. It is listed in `failed_peers` as `{node_id, name, error_code}`, where `error_code` is `timeout`, `unreachable`, `peer_address_refused` (see [Peer URL Policy](configuration.md#peer-url-policy)), `key_unavailable` (the peer's stored `api_key` does not open with this node's federation secret; set it again), `outbound_rate_limited`, `bad_response`, the peer's error code when it is one a Memora node sends on its peer routes (`federation_unauthorized`, `federation_peer_revoked`, `federation_loop`, `rate_limited`, `invalid_input`, …), or otherwise `http_<status>`; `partial_success` is then `true`. A peer's hits are checked before they are merged: a hit whose `memory_id`, `workspace_id`, `cell_id`, `watermark` or `written_by_agent_id` is not a UUID is dropped, at most `per_peer_k` hits are kept, `content` is cut to `max_content_bytes` (and dropped when `include_content` was not asked for), excerpts are bounded, and `node_id`, `node_name` and `via` always name the peer as this node has it configured. The search's `federation_search` ledger entry counts dropped hits per peer in `dropped_hits`. `peer_latency_ms` maps each answering node ID, this node's included, to milliseconds. `federated` cannot be combined with `workspace_ids`, because peers search their whole node, and a `multi-tenant` node refuses it with `403 federation_multi_tenant`. On a node without federation, `federated: true` is `400`. The MCP `memora_search` tool takes the same fields.

### Fetching a Memory From Its Node

`GET /memories/{memory_id}` returns a full Memory with its Cells, `workspace_id`, `node_id` and `node_name`, looked up across every workspace on this node. With `?node={node_id}` naming a peer, this node fetches it from that peer with the peer's key and timeout. That is how to read a whole hit from a federated search. `node` set to this node's ID is the same as omitting it.

| Status | Error code | Cause |
|---|---|---|
| `404` | `not_found` | The memory is not on that node, or was forgotten. |
| `404` | `unknown_node` | `node` is not a peer of this node, is `revoked`, or has no `api_key`. |
| `502` | `timeout`, `unreachable`, the peer's error code, … | The peer did not answer. The code is classified as for `failed_peers`. |

In `multi-tenant` mode the `X-Memora-Workspace` header is required, only that workspace's memories are returned, and `node` cannot name a peer (`403 federation_multi_tenant`). The SDK's `FetchMemory` and the MCP `memora_fetch` tool do the same.

### Federation Audit and Metrics

Every federated call is written to the ledger as a node-level entry (no workspace), with the other node's ID in `api_key_id`, so the ledger's `actor` filter finds everything about one peer:

| Op | Written by | Metadata |
|---|---|---|
| `federation_search` | the node that searched | `query`, `peers_asked`, `hits_per_node`, `total_hits`, `failed_peers`, `dropped_hits` |
| `federation_inbound` | the node that answered a peer's search | `requesting_peer`, `query`, `hit_count`, `federation_path` |
| `federation_rejected` | the node that refused a peer call | `requesting_peer` (when well-formed), `reason`: `unknown_peer`, `bad_key`, `bad_cert`, `revoked`, `federation_mismatch`, `loop` or `rate_limited`; `target` is the route template, never the request path. `unknown_peer` and `bad_key` refusals are summarised: one entry per client address (IPv4, or IPv6 /64) and route per minute (`bad_key` also per claimed peer), with `count`, `client`, `first_seen`, `last_seen` and, for `unknown_peer`, up to 5 claimed node IDs in `sample_claimed_peers` |
| `federation_fetch` | both nodes of a fetch | `direction` (`outbound`/`inbound`), `node_id`, `memory_id`, `outcome`; the serving node files it under the memory's workspace |
| `federation_peer` | the node whose peer registry changed (API or console) | `action` (`create`, `update`, `delete`), `node_id`, `name`, `url`, `previous_url` when it changed, `api_key_set`, `inbound_key_set`, `revoked`; keys are never recorded |

The query text is sensitive. It is kept in the entry's metadata, so redacting an entry's `metadata` hides it. The reason for a refusal is recorded only in the ledger; the refused caller sees the same `401` whatever the reason.

`GET /metrics` adds, when federation is on:

| Metric | Labels |
|---|---|
| `memora_federation_peer_requests_total` | `peer` (node ID), `outcome`: `ok`, `timeout`, `unreachable`, `rate_limited`, `rejected`, `not_found`, `error`, `key_unavailable` |
| `memora_federation_peer_latency_seconds` | `peer`; a histogram of this node's calls to that peer |
| `memora_federation_inbound_total` | `peer` (node ID, or `unknown` for an unregistered caller), `outcome`: `ok`, `error`, or a refusal reason |

### Managing Peers

The peer registry can be edited while the node runs. These routes take the API key; peers cannot call them. An edit takes effect on the next request: a new peer is searched, and a removed peer is no longer searched and its calls get `401`. A revoked peer stays listed, is not searched, and its calls get `403`.

| Field | Value |
|---|---|
| `GET /federation/peers` | List peers: `node_id`, `name`, `url`, `trust_mode`, `has_inbound_key`, `has_api_key` (`false`, with `api_key_status: "unreadable"`, when the stored key does not open with this node's federation secret), `client_cert_sha256`, `timeout_ms`, `rate_limit_qps`, `revoked`, `created_at`, `updated_at`. Keys are never returned. |
| `POST /federation/peers` | Create or replace the peer with `node_id`. Body: `node_id`, `name`, `url`; optional `inbound_key` (the key the peer will present, stored only as its SHA-256) or `inbound_key_sha256`; `api_key` (the key this node presents to the peer, stored sealed); `client_cert_sha256` (needs `server.tls`); `timeout_ms`; `rate_limit_qps` (this node's calls to the peer); `inbound_qps` (the peer's calls to this node; default `federation.inbound_qps`); `revoked`. A key left out keeps the stored one, except that changing `url` needs `api_key` again (`400` otherwise), so a stored key is never sent to a new host. `url` must pass the node's [Peer URL Policy](configuration.md#peer-url-policy), which is checked against the addresses the host resolves to; `400` otherwise. TLS file paths and the URL policy are set only in the config file. Answers with the peer as `GET` shows it. |
| `DELETE /federation/peers/{node_id}` | Remove a peer. `404` if it is not registered. |

Without federation these answer `404 federation_disabled`. The console's Federation page uses the same registry. Every change is written to the ledger as a `federation_peer` entry.

Anyone holding the node's API key can change its peers, and with them where the node sends federated queries. Treat the API key as an administrative credential on a federated node. The URL policy, which only the config file sets, bounds where a runtime peer can point.

### Federation

Other nodes call these routes. They take peer credentials, not the API key; see [Peer Credentials](configuration.md#peer-credentials).

| Field | Value |
|---|---|
| `GET /federation/memories/{memory_id}` | A peer reads a full Memory, with its Cells, from any workspace on this node. `404` if it is not here. |
| `POST /federation/search` | A peer searches every workspace on this node. Body: `query`, `mode`, `k`, `include_content`, `max_content_bytes`, with the same meanings as `POST /search`. Any other field, including `workspace_ids`, is refused with `400`. This node embeds the query text with its own embedder, so peers can use different models. Hits are those of `POST /search` plus `node_id` and `node_name`, which identify this node. The node does not forward the query to its own peers. |

Before any credential is checked, each client address may send at most twice `federation.inbound_qps` (at least 200) requests per second to these routes; past that it gets `429 rate_limited` with `Retry-After`, and nothing is written to the ledger. Peers behind one NAT or proxy share that limit. A malformed path is refused by the route table before authentication, and request lines and headers over 64 KiB get `431`. A peer that sends more than its `inbound_qps` (or `federation.inbound_qps`, default 100) requests per second gets `429 rate_limited` with a `Retry-After` header. A node in `multi-tenant` mode refuses with `403 federation_multi_tenant`, because peers are trusted per node, not per tenant. Loop and rate-limit refusals are written to the ledger as `federation_rejected` entries with no workspace.

### Operational Endpoints

These sit outside `/v1`.

| Field | Value |
|---|---|
| `GET /healthz` | Liveness. Returns `200` whenever the process is up. |
| `GET /readyz` | Readiness. Pings the metadata, vector, and ledger adapters. `stale_vectors` counts vectors from another embedding model, and `missing_vectors` the cells of live memories with no vector from the active one; vector search reaches neither until `memora-core reembed`. Both are integers at the top level, and strings under `adapter` as before. |

These are ordinary non-`/ui` paths, so they require the bearer token when an API key is configured. A probe that omits it receives `401`.

## The MCP Server

`memora-core mcp` speaks JSON-RPC 2.0 over stdio. Setting `mcp_enable` on a running server additionally mounts a WebSocket endpoint at `/mcp`. The server advertises protocol version `2024-11-05` and implements `tools/list` and `tools/call`. Twenty-seven tools are exposed.

| Section | Description |
|---|---|
| Workspaces | `memora_create_workspace`, `memora_get_workspace`, `memora_list_workspaces`, `memora_update_workspace`, `memora_delete_workspace` |
| Collections | `memora_create_collection`, `memora_list_collections`, `memora_delete_collection` |
| Memories | `memora_imprint`, `memora_lookup`, `memora_fetch`, `memora_update`, `memora_patch`, `memora_append`, `memora_forget`, `memora_list_memories`, `memora_get_watermark_history` |
| Recall | `memora_search`, `memora_recall`, `memora_pin`, `memora_unpin` |
| Graph | `memora_link`, `memora_unlink`, `memora_neighbors`, `memora_traverse` |
| Agents | `memora_register_agent`, `memora_list_agents` |

> **The stdio transport is unauthenticated unless you give it a key.** With no key configured it treats its caller as trusted — it only talks to the process that started it — and logs a warning saying so. Passing `--api-key`, or setting `MEMORA_API_KEY`, makes the `initialize` handshake require that key and rejects every other method until the handshake succeeds.

There is no MCP tool for graph statistics or for querying the ledger; both are REST-only. Everything else in the tool list maps onto the REST verb of the same name.

## The Go SDK

`pkg/client` is the Go client the CLI is built on, and it can be imported directly. Transport is REST.

```go
import (
    "context"

    "github.com/axiom-studio/memora/pkg/client"
    "github.com/axiom-studio/memora/pkg/types/api"
)

c := client.New("http://localhost:7777", apiKey)
c.AgentID = "agent_my_service"

res, err := c.Imprint(ctx, workspaceID, api.ImprintRequest{
    Content: "Customer wants the enterprise plan.",
})
```

`New` returns a client with a 30-second timeout. `Endpoint`, `APIKey`, `AgentID`, `Workspace`, and `HTTPClient` are all exported, so the HTTP client can be replaced for connection pooling, tracing, or tests. Every method takes the workspace as an explicit argument; the `Workspace` field is a convenience holder for callers that want one place to keep it, not an implicit default.

| Section | Description |
|---|---|
| Memories | `Imprint`, `Lookup`, `Update`, `Patch`, `Append`, `Forget`, `ListMemories`, `WatermarkHistory` |
| Recall | `Recall`, `CreatePin`, `ListPins`, `DeletePin` |
| Graph | `Link`, `Unlink`, `Neighbors`, `Traverse`, `GraphStats` |
| Containers | `CreateWorkspace`, `GetWorkspace`, `ListWorkspaces`, `DeleteWorkspace`, `CreateCollection`, `ListCollections` |
| Identity | `RegisterAgent`, `ListAgents` |
| Audit | `LedgerQuery` |
| Health | `Health`, `Ready` |

### Handling Errors

Non-2xx responses come back as a `*client.APIError` carrying the status, the decoded envelope, and the raw body. Two helpers cover the cases worth branching on:

```go
res, err := c.Patch(ctx, workspaceID, memoryID, watermark, req)
if client.IsCASErr(err) {
    // Someone else wrote first. Re-read and decide.
}

var apiErr *client.APIError
if errors.As(err, &apiErr) && apiErr.IsNotFound() {
    // The Memory is gone.
}
```

The SDK sets `Authorization`, `Memora-Agent-Id`, and `If-Match` from the client's fields and method arguments. It does not set `Memora-Identity-Provider`, so SDK writes are always verified by the default `opaque` provider.

## Error Codes

Every error response carries an `error_code`. Client faults are distinguished from server faults, so a `5xx` always indicates a Memora problem rather than a bad request.

| Status | Description |
|---|---|
| `invalid_input` | 400 — malformed body, an unknown field, an unknown recall mode, or a missing required field. |
| `patch_anchor_not_found` | 400 — a patch `old_string` does not appear in the current content. |
| `graph_traverse_depth_exceeded` | 400 — the requested traversal depth exceeds the graph adapter's cap, which is `3` in the SQLite graph store. |
| `missing_agent_id` | 400 — a write arrived without the `Memora-Agent-Id` header. |
| `unauthorized` | 401 — missing or invalid bearer token. |
| `agent_verification_failed` | 401 — the identity provider rejected the agent. |
| `federation_unauthorized` | 401 — a peer route was called with an unknown node ID or a missing or wrong peer key or client certificate. |
| `federation_peer_revoked` | 403 — the calling peer is revoked. |
| `federation_multi_tenant` | 403 — federation search is refused in `multi-tenant` mode. |
| `federation_disabled` | 404 — federation is not enabled on this node. |
| `not_found` | 404 — the workspace, collection, memory, edge, or agent does not exist. |
| `method_not_allowed` | 405 — the endpoint exists but not for this HTTP method. |
| `already_exists` | 409 — the record already exists. |
| `already_linked_via_synthetic` | 409 — an auto-generated edge already joins these Memories. |
| `cannot_unlink_synthetic_edge` | 409 — `vector_neighbor` edges cannot be removed by hand. |
| `not_empty` | 409 — the container still holds records. |
| `federation_loop` | 409 — this node is already in the request's federation path. |
| `cas_conflict` | 412 — the supplied watermark does not match the current head. |
| `quota_exceeded` | 429 — a configured limit was hit. |
| `rate_limited` | 429 — a peer exceeded `federation.inbound_qps`. `Retry-After` gives the wait in seconds. |
| `internal_error` | 500 — an unclassified server fault. |
| `capability_unavailable` | 501 — the configured adapter does not implement the operation. Most often a missing GraphStore, or a LedgerStore that cannot be queried. |

`capability_unavailable` is the one to expect during deployment rather than at runtime: it means the request is well-formed but the storage seam behind it was never configured. See [Adapters](adapters.md) for which operations depend on which seam.
