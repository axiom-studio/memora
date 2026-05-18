# Architecture

Memora Core is a single Go module that produces two binaries —
`memora-core` (server) and `memora-cli` (client) — and a small set of
public Go packages third parties can vendor.

```
                                ┌───────────────────────────────┐
              ┌──── HTTP ──────▶│                               │
              │                 │     memora-core (server)      │
   memora-cli │                 │  ┌─────────────────────────┐  │
              │                 │  │   internal/server/http  │  │
              │                 │  │   internal/mcp (stdio)  │  │
              │                 │  └────────────┬────────────┘  │
              │                 │               │               │
              │                 │  ┌────────────▼────────────┐  │
              └──── MCP ───────▶│  │    internal/service     │  │
              (stdio JSON-RPC)  │  │  (chunker · embed pool) │  │
                                │  └────────────┬────────────┘  │
                                │               │               │
                                │  ┌────────────▼────────────┐  │
                                │  │     pkg/adapter         │  │
                                │  │  PrimaryStore / Vector  │  │
                                │  │      / Ledger / ID      │  │
                                │  └─┬──────┬──────┬──────┬──┘  │
                                │    │      │      │      │     │
                                └────┼──────┼──────┼──────┼─────┘
                                     │      │      │      │
                            ┌────────▼──┐   │      │      │
                            │  sqlite   │   │      │      │
                            │ + sqlite- │   │      │      │
                            │  vec      │   │      │      │
                            │ + sqlite  │   │      │      │
                            │   ledger  │   │      │      │
                            └───────────┘   │      │      │
                                            ▼      ▼      ▼
                                       (postgres, pgvector, file)
                                        future-shipped adapters
```

## Public package layout

| Package | Role |
|---|---|
| `pkg/types` | Domain types (Workspace, Memory, Cell, Edge, Agent, Watermark, ...). |
| `pkg/types/api` | REST request / response wire types (Imprint, Patch, Recall, ...). |
| `pkg/adapter` | Three pluggable persistence interfaces — PrimaryStore, VectorStore, LedgerStore — plus IdentityProvider and the driver registry. |
| `pkg/embedding` | EmbeddingProvider contract + noop / openai providers. |
| `pkg/identity` | OSS identity providers (opaque, anthropic_session) + standards stubs. |
| `pkg/client` | Go SDK over the REST surface; what memora-cli is built on. |

## Private package layout

| Package | Role |
|---|---|
| `internal/store/sqlite` | Default OSS PrimaryStore. |
| `internal/store/sqlitevec` | Default OSS VectorStore (pure-Go cosine; vec0 in v0.5). |
| `internal/ledger/sqlite` | Default OSS LedgerStore. |
| `internal/ledger/file` | Flat-file JSONL+gzip rotation LedgerStore. |
| `internal/chunker` | Token-aware default + markdown + no-chunk chunkers. |
| `internal/embed/queue` | Async worker pool + the `text_md5` skip-re-embed moat. |
| `internal/service` | Business-logic layer (Imprint / Update / Patch / Append / Recall / Forget / graph traversal helpers). |
| `internal/server/http` | net/http REST surface — middleware, routes, error envelope. |
| `internal/mcp` | Bundled MCP JSON-RPC 2.0 stdio server (25 OSS tools). |

## The patch-mode moat

The economic moat lives in `internal/embed/queue.EmbedNow` and is
invoked by `internal/service.Patch`. After the patch's diff ops apply
atomically:

1. The post-patch content is re-chunked.
2. Each new cell's `text_md5` is computed.
3. Cells whose `text_md5` is unchanged AND already have a
   `vector_key` are **preserved verbatim** — no Provider.Embed call,
   no `VectorStore.PutVector`, no `Primary.UpdateCellVectorKey`.
4. Only changed cells get re-embedded.

`PatchResponse.cells_re_embedded` / `cells_skipped` make the moat
metric visible on the wire — both via the REST PATCH endpoint and the
MCP `memora_patch` tool result.

## Context Graph

Memories within a Workspace form an explicit, typed, directed graph
stored in `memora_edges`. The closed edge_type enum is `parent_of`,
`derived_from`, `supersedes`, `references`, `session_of`, `mentions`
(see `pkg/types/edge.go`). Caller-declared, never extracted from
content.

Six PrimaryStore methods power the graph: `GraphLink`, `GraphUnlink`,
`GraphLinkBatch`, `GraphNeighbors`, `GraphTraverse`, and
`GraphCascadeForget`. Traversal is BFS with an OSS depth cap of 3,
a `MaxEdges`/`Budget` safety budget, and an optional Memory predicate
filter.

`Recall.graph_expansion` wires the graph back into search: seeds come
from keyword/vector/hybrid mode, then BFS walks expand the seed set
with `score = weight × decay^layer × seed_score`. Results carry
`via: "seed"|"graph"` and a `graph_provenance` block when graph-derived.

## Adapter contracts

The three adapter contracts in `pkg/adapter` are the public OSS API.
Third parties writing adapters import only this package — they don't
need to depend on any `internal/` package.

```go
type PrimaryStore interface {
    Open(ctx, PrimaryConfig) error
    // Workspace / Collection / Memory / Cell CRUD with CAS via watermark.
    // Agent registry.
    // Context Graph (Link / Unlink / LinkBatch / CascadeForget /
    //                Neighbors / Traverse / Stats).
    // Watermark history.
    Capabilities() PrimaryCapabilities
}

type VectorStore interface {
    PutVector / PutVectorsBatch / Query / DeleteVectors
    Capabilities() VectorCapabilities
}

type LedgerStore interface {
    Append / AppendBatch / Query / Redact
    Capabilities() LedgerCapabilities
}

type IdentityProvider interface {
    Name() string
    Verify(ctx, IdentityVerifyInput) error
    Capabilities() IdentityCapabilities
}
```

Drivers register themselves in `init()` via
`adapter.RegisterPrimary("driver-name", factory)` (and the equivalent
for vector/ledger/identity). The server's `--primary-driver` flag
(or `MEMORA_PRIMARY_DRIVER`) selects which driver to instantiate at
boot.

See [adapter-authoring.md](adapter-authoring.md) for the step-by-step
guide.
