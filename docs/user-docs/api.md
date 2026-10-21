# API Reference

Memora exposes the same service layer through three programmable surfaces: a REST API under `/v1`, an MCP server for agent clients, and a Go SDK. This page lists the endpoints, tools, and methods of each, plus the error codes common to all of them.

## Request Conventions

Request and response bodies are JSON. Errors use one envelope shape across every endpoint:

```json
{
  "error_code": "cas_conflict",
  "message": "expected watermark wmk_… but head is wmk_…",
  "details": {}
}
```

Unknown fields in a request body are rejected rather than ignored, so a misspelled key surfaces as `400 invalid_input` instead of being silently dropped.

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
| `POST /search` | Search every workspace on the node, or those in `workspace_ids`. Body: `query`, `mode` (`keyword`, `vector`, or `hybrid`, the default), `k` (default 5), `workspace_ids`, `filters`, `include_content`, `max_content_bytes` (default 64 KiB, at most 1 MiB), `include_cells`. Each hit carries its `workspace_id`, `matched_text` (the full text of the matching Cell, located by `cell_id` and `cell_index`), a short `text` excerpt, `tags`, `written_by_agent_id` and `created_at`. With `include_content` it also carries the Memory's `content`, with `content_truncated` set if it was cut. Recall accepts the same three options. In `multi-tenant` mode the `X-Memora-Workspace` header is required (`400 missing_workspace` without it) and the search stays inside that workspace. With `federated: true` the node also searches its federation peers; see [Federated Search](#federated-search). |
| `POST /workspaces/{ws}/recall` | Run a recall query. |
| `GET /workspaces/{ws}/recall/pins` | List saved queries. |
| `POST /workspaces/{ws}/recall/pins` | Save a query. `mode` defaults to `hybrid` and `k` to 10. |
| `DELETE /workspaces/{ws}/recall/pins/{id}` | Delete a saved query. |
| `GET /workspaces/{ws}/agents` | List agents. |
| `POST /workspaces/{ws}/agents` | Register an agent. |
| `GET /workspaces/{ws}/agents/{id}` | Read an agent. |
| `DELETE /workspaces/{ws}/agents/{id}` | Deactivate an agent. |
| `GET /workspaces/{ws}/ledger` | Query the audit ledger. Returns `501` if the configured ledger cannot query. |

Listings are ordered by creation time, newest first, with the ID breaking ties, so pages stay stable while new records arrive: a new record appears on the first page and never shifts later pages. Cursors are opaque; a malformed one returns `400 invalid_input`. The SDK's `ListWorkspacesPage` and `ListMemoriesPage` return the next cursor; `ListWorkspaces` and `ListMemories` return the first page. The MCP list tools return the first page only.

Ledger queries accept `since`, `until`, `actor`, `agent_id`, `memory_id`, `edge_id`, `op` (comma-separated), `limit`, and `since_ledger_id` for cursor paging.

### Federated Search

`POST /search` with `federated: true` searches this node and, in parallel, each of its peers' `POST /federation/search`. Each peer embeds the query with its own model.

| Field | Value |
|---|---|
| `federated` | `true` to include peers. |
| `peers` | Node IDs to ask. Default: every peer that is not `revoked` and has an `api_key`. An unknown or unusable node ID is `400`. |
| `per_peer_k` | How many hits each peer returns. Default `k`. |
| `timeout_ms` | Bounds every peer. Default: each peer's `timeout_ms` (see [Peer Credentials](configuration.md#peer-credentials)). At most `60000`. |

Hits carry `node_id` and `node_name`, and peer hits have `via: "federation:<peer name>"`. A peer that fails, times out or is over this node's outbound limit for it does not fail the search. It is listed by name in `failed_peers`, and `partial_success` is `true`. `peer_latency_ms` maps each answering node ID, and `local`, to milliseconds. `federated` cannot be combined with `workspace_ids`, because peers search their whole node, and a `multi-tenant` node refuses it with `403 federation_multi_tenant`. On a node without federation, `federated: true` is `400`. The MCP `memora_search` tool takes the same fields.

### Federation

Other nodes call these routes. They take peer credentials, not the API key; see [Peer Credentials](configuration.md#peer-credentials).

| Field | Value |
|---|---|
| `POST /federation/search` | A peer searches every workspace on this node. Body: `query`, `mode`, `k`, `include_content`, `max_content_bytes`, with the same meanings as `POST /search`. Any other field, including `workspace_ids`, is refused with `400`. This node embeds the query text with its own embedder, so peers can use different models. Hits are those of `POST /search` plus `node_id` and `node_name`, which identify this node. The node does not forward the query to its own peers. |

A peer that sends more than `federation.inbound_qps` requests per second (default 100) gets `429 rate_limited` with a `Retry-After` header. A node in `multi-tenant` mode refuses with `403 federation_multi_tenant`, because peers are trusted per node, not per tenant. Loop and rate-limit refusals are written to the ledger as `federation_rejected` entries with no workspace.

### Operational Endpoints

These sit outside `/v1`.

| Field | Value |
|---|---|
| `GET /healthz` | Liveness. Returns `200` whenever the process is up. |
| `GET /readyz` | Readiness. Pings the metadata, vector, and ledger adapters. |

These are ordinary non-`/ui` paths, so they require the bearer token when an API key is configured. A probe that omits it receives `401`.

## The MCP Server

`memora-core mcp` speaks JSON-RPC 2.0 over stdio. Setting `mcp_enable` on a running server additionally mounts a WebSocket endpoint at `/mcp`. The server advertises protocol version `2024-11-05` and implements `tools/list` and `tools/call`. Twenty-five tools are exposed.

| Section | Description |
|---|---|
| Workspaces | `memora_create_workspace`, `memora_get_workspace`, `memora_list_workspaces`, `memora_update_workspace`, `memora_delete_workspace` |
| Collections | `memora_create_collection`, `memora_list_collections`, `memora_delete_collection` |
| Memories | `memora_imprint`, `memora_lookup`, `memora_update`, `memora_patch`, `memora_append`, `memora_forget`, `memora_list_memories`, `memora_get_watermark_history` |
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
