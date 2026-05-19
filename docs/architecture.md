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
                                │  │  Metadata / Content /   │  │
                                │  │  Vector / Graph / Ledger│  │
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
| `pkg/adapter` | Five pluggable persistence interfaces — MetadataStore, ContentStore, VectorStore, LedgerStore, GraphStore — plus IdentityProvider and the driver registry. |
| `pkg/embedding` | EmbeddingProvider contract + noop / openai providers. |
| `pkg/identity` | OSS identity providers (opaque, anthropic_session) + standards stubs. |
| `pkg/client` | Go SDK over the REST surface; what memora-cli is built on. |

## Private package layout

| Package | Role |
|---|---|
| `internal/store/sqlite` | Default OSS MetadataStore. |
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

The `GraphStore` adapter (extracted from the retired `PrimaryStore`)
exposes six methods: `Link`, `Unlink`, `LinkBatch`, `Neighbors`,
`Traverse`, and `CascadeForget`. Traversal is BFS with an OSS depth
cap of 3, a `MaxEdges`/`Budget` safety budget, and an optional Memory
predicate filter. The service layer nil-guards every `GraphStore`
call — deployments without a graph backend degrade gracefully (forget
skips cascade; explicit graph ops return 501).

`Recall.graph_expansion` wires the graph back into search: seeds come
from keyword/vector/hybrid mode, then BFS walks expand the seed set
with `score = weight × decay^layer × seed_score`. Results carry
`via: "seed"|"graph"` and a `graph_provenance` block when graph-derived.

## VectorStore performance trade-off

v0.1 ships a **pure-Go cosine-similarity scan** (`internal/store/sqlitevec/`)
rather than the originally-specified `vec0` virtual-table extension.

**Why:** `modernc.org/sqlite` — the pure-Go SQLite driver that keeps
`CGO_ENABLED=0` — cannot load C extensions. `vec0` is a C extension.
Preserving zero-CGO was a hard requirement for the cross-platform
release pipeline (see `.goreleaser.yaml`), so the VectorStore ships a
brute-force exact scan instead of sub-linear ANN.

**What this means in practice:**

| Metric | Pure-Go scan (v0.1) | vec0 ANN (v0.5+) |
|---|---|---|
| Complexity | O(n) per query | O(log n) via HNSW/IVF |
| Corpus ceiling | ~100K vectors (single-digit ms on modern hardware) | Millions |
| `SupportsANN` | `false` | `true` |
| Filter strategy | Post-scan Go filtering | WHERE-clause pushdown into the index |
| CGO requirement | None | `CGO_ENABLED=1` + C toolchain |

For v0.1's expected corpus sizes (hundreds to low-thousands of vectors
per workspace), the brute-force scan is functionally indistinguishable
from ANN — both return in <1 ms. The adapter interface is unchanged;
swapping in vec0 later is a drop-in driver replacement with no
caller-visible API change.

**Tracked follow-up:** vec0 integration targeting v0.5 (requires either
a CGO build variant or a pure-Go vec0 port).

## Adapter contracts

The five adapter contracts in `pkg/adapter` are the public OSS API.
Third parties writing adapters import only this package — they don't
need to depend on any `internal/` package.

```go
type MetadataStore interface {
    Open(ctx, MetadataConfig) error
    // Workspace / Collection / Memory / Cell CRUD with CAS via watermark.
    // Agent registry. Watermark history.
    Capabilities() MetadataCapabilities
}

type ContentStore interface {
    Open(ctx, ContentConfig) error
    // PutMemoryContent / PutCellContent / GetMemoryContent / GetCellContent /
    // DeleteAllForMemory
    Capabilities() ContentCapabilities
}

type GraphStore interface {
    Open(ctx, GraphConfig) error
    // Link / Unlink / LinkBatch / CascadeForget /
    // Neighbors / Traverse / Stats
    Capabilities() GraphCapabilities
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
`adapter.RegisterMetadata("driver-name", factory)` (and the equivalent
for content/graph/vector/ledger/identity). The server's
`--metadata-driver` flag (or `MEMORA_METADATA_DRIVER`) selects which
metadata driver to instantiate at boot; `--graph-driver`, `--vector-driver`,
etc. select the others.

See [adapter-authoring.md](adapter-authoring.md) for the step-by-step
guide.
