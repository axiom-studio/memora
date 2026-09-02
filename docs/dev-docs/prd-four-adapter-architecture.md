# PRD Extension: Five-Adapter Architecture

**Extends:** PRD #297 (Memora Core OSS Edition), PRD #298 (S3 Vectors Deep-Dive)
**Status:** Draft — awaiting human triage
**Author:** Principal Engineer (automated)
**Date:** 2026-05-19

---

## 1. Executive Summary

Memora's v0.1 persistence layer collapses four distinct concerns into one monolithic PrimaryStore adapter: metadata, content, graph topology, and (partially) vector embeddings. This document proposes extracting them into five independent adapter interfaces, each backed by purpose-built storage:

| Adapter | Responsibility | OSS Default | Cloud Default |
|---------|---------------|-------------|---------------|
| **ContentStore** | Memory + Cell text bodies | SQLite / file | S3 (object store) |
| **MetadataStore** | Workspaces, Collections, Tags, Agents, Cells metadata, Watermarks | SQLite | PostgreSQL |
| **VectorStore** | ANN index for embedding search | sqlite-vec | pgvector / S3 Vectors |
| **GraphStore** | Context Graph edges + traversal | SQLite (virtual table) | PostgreSQL (recursive CTE) / Neo4j |
| **LedgerStore** | Append-only audit log | SQLite | PostgreSQL / managed event store |

The wire API (MCP tools, REST verbs) does NOT change. The split is internal; agents calling Imprint/Patch/Recall are unaffected.

---

## 2. Current State (v0.1)

### 2.1 Three-Adapter Architecture

```
┌──────────────────────────────────────────────┐
│  PrimaryStore (sqlite)                       │
│  ┌───────┐ ┌──────┐ ┌──────┐ ┌───────────┐  │
│  │Memory │ │Cells │ │Graph │ │ Workspaces│  │
│  │content│ │.text │ │edges │ │Collections│  │
│  │ TEXT  │ │ TEXT │ │      │ │Tags,Agents│  │
│  └───────┘ └──────┘ └──────┘ └───────────┘  │
└──────────────────────────────────────────────┘
┌────────────────────┐  ┌──────────────────────┐
│  VectorStore       │  │  LedgerStore         │
│  (sqlite-vec)      │  │  (sqlite)            │
└────────────────────┘  └──────────────────────┘
```

**Where content lives today:**
- `memora_memories.content TEXT NOT NULL` — whole-document body
- `memora_cells.text TEXT NOT NULL` — per-chunk text
- FTS5 virtual table (`memora_cells_fts`) mirrors cell text for keyword search

**Problems with co-location:**
1. **Scaling ceiling.** SQLite single-writer limits throughput for large content writes. PostgreSQL's TOAST transparently handles large values but competes for shared_buffers with metadata queries.
2. **Backup granularity.** You cannot back up content independently of metadata. Object stores offer versioning/lifecycle rules that RDBMS lacks.
3. **Cost.** Content is read-rarely (recall returns cell IDs, not full text until explicitly fetched). Storing it in the same tier as hot metadata wastes IOPS.
4. **Compliance.** GDPR Art. 17 erasure is complicated when content is interleaved with metadata in the same table.

### 2.2 ContentStore (Shipped in v0.3 / F13.T1)

Commit `9f29b51` introduced `pkg/adapter/content.go` as an **additive** fourth adapter:
- Interface: `ContentStore` with 10 methods (Put/Get/Delete for memory + cell + batch + cascade)
- Drivers: `sqlite` and `file`
- Integration: dual-write from service layer (content-first, then legacy)
- Backward compat: legacy `content TEXT` column remains authoritative for reads; ContentStore is write-ahead only

This validates the extraction pattern and proves the dual-write approach works without breaking the wire API.

---

## 3. Proposed Five-Adapter Architecture

### 3.1 ContentStore

**Already defined** at `pkg/adapter/content.go`. Interface stable since F13.T1.

```go
type ContentStore interface {
    Open(ctx context.Context, cfg ContentConfig) error
    Close() error
    Ping(ctx context.Context) error
    Capabilities() ContentCapabilities

    PutMemoryContent(ctx context.Context, workspaceID, memoryID, contentMD5, content string) error
    GetMemoryContent(ctx context.Context, workspaceID, memoryID string) (string, error)
    DeleteMemoryContent(ctx context.Context, workspaceID, memoryID string) error

    PutCellContent(ctx context.Context, workspaceID, memoryID, cellID, textMD5, text string) error
    GetCellContent(ctx context.Context, workspaceID, memoryID, cellID string) (string, error)
    GetCellContentBatch(ctx context.Context, workspaceID, memoryID string, cellIDs []string) (map[string]string, error)
    DeleteCellContent(ctx context.Context, workspaceID, memoryID, cellID string) error

    DeleteAllForMemory(ctx context.Context, workspaceID, memoryID string) error
}
```

**Candidate drivers:**

| Driver | Edition | Status | Notes |
|--------|---------|--------|-------|
| sqlite | OSS | Shipped (v0.3) | Co-located with metadata DB |
| file | OSS | Shipped (v0.3) | Local filesystem, 0o600 perms |
| postgres | OSS | Planned (v0.5) | `bytea` or `TEXT` in dedicated table |
| s3 | Cloud | Planned (v0.5) | Default for Cloud; content-addressed keys |

**Open questions:**
- Should `contentMD5` be enforced as a content-addressed key (dedup within a workspace)? Current interface accepts it but doesn't enforce uniqueness.
- Does the FTS5 index follow content to the ContentStore, or does keyword search remain a MetadataStore concern backed by a denormalized index?

### 3.2 MetadataStore (extracted from PrimaryStore)

MetadataStore owns the structural/relational data that doesn't belong in content, graph, or vector indexes:

```go
type MetadataStore interface {
    Open(ctx context.Context, cfg MetadataConfig) error
    Close() error
    Ping(ctx context.Context) error
    Capabilities() MetadataCapabilities

    // Workspace CRUD
    CreateWorkspace(ctx context.Context, w *types.Workspace) error
    GetWorkspace(ctx context.Context, id string) (*types.Workspace, error)
    ListWorkspaces(ctx context.Context, limit int) ([]types.Workspace, error)
    UpdateWorkspace(ctx context.Context, w *types.Workspace) error
    DeleteWorkspace(ctx context.Context, id string) error

    // Collection CRUD
    CreateCollection(ctx context.Context, c *types.Collection) error
    GetCollection(ctx context.Context, id string) (*types.Collection, error)
    ListCollections(ctx context.Context, workspaceID string) ([]types.Collection, error)
    DeleteCollection(ctx context.Context, id string) error

    // Memory metadata (NOT content — content lives in ContentStore)
    ImprintMemory(ctx context.Context, m *types.Memory) (watermark string, err error)
    GetMemory(ctx context.Context, id string) (*types.Memory, error)
    GetMemoryAtWatermark(ctx context.Context, id, watermark string) (*types.Memory, error)
    ListMemories(ctx context.Context, workspaceID, collectionID string, limit int) ([]types.Memory, error)
    UpdateMemory(ctx context.Context, id, expectedWatermark string, m *types.Memory) (newWatermark string, err error)
    ForgetMemory(ctx context.Context, id string) error

    // Cell metadata (seq, hash, vector_key — NOT text)
    UpsertCells(ctx context.Context, memoryID string, cells []types.Cell) error
    GetCells(ctx context.Context, memoryID string) ([]types.Cell, error)
    UpdateCellVectorKey(ctx context.Context, cellID, vectorKey, embeddingModel string) error
    FlipRecallReadyIfAllEmbedded(ctx context.Context, memoryID string) (bool, error)

    // Watermark history
    GetWatermarkHistory(ctx context.Context, workspaceID, targetID string, since time.Time) ([]types.WatermarkHistoryEntry, error)
    AppendWatermarkHistory(ctx context.Context, entry types.WatermarkHistoryEntry) error

    // Tags
    UpsertTag(ctx context.Context, workspaceID, memoryID, key, value string) error
    DeleteTag(ctx context.Context, workspaceID, memoryID, key string) error

    // Agent registry
    RegisterAgent(ctx context.Context, a *types.Agent) error
    GetAgent(ctx context.Context, workspaceID, id string) (*types.Agent, error)
    ListAgents(ctx context.Context, workspaceID string, limit int) ([]types.Agent, error)
    DeactivateAgent(ctx context.Context, workspaceID, id string) error

    // Keyword search (FTS) — debatable; may move to ContentStore
    KeywordSearch(ctx context.Context, workspaceID, query string, k int) ([]types.CellMatch, error)
}
```

**Key change from PrimaryStore:** `types.Memory.Content` is no longer stored here. The field remains on the struct for wire-API compat (populated by joining with ContentStore), but MetadataStore stores only `content_md5` as a reference.

**Candidate drivers:**

| Driver | Edition | Status | Notes |
|--------|---------|--------|-------|
| sqlite | OSS | Current (rename from PrimaryStore) | Single-file, WAL mode |
| postgres | OSS | Current (rename from PrimaryStore) | Shared-nothing possible via Citus |

### 3.3 VectorStore (unchanged)

The existing `VectorStore` interface is already cleanly separated. No structural changes needed.

```go
type VectorStore interface {
    Open(ctx context.Context, cfg VectorConfig) error
    Close() error
    Ping(ctx context.Context) error
    Capabilities() VectorCapabilities

    PutVector(ctx context.Context, put VectorPut) error
    PutVectorsBatch(ctx context.Context, puts []VectorPut) error
    Query(ctx context.Context, q VectorQuery) ([]VectorHit, error)
    DeleteVectors(ctx context.Context, keys []VectorKey) error
}
```

**Candidate drivers:**

| Driver | Edition | Status | Notes |
|--------|---------|--------|-------|
| sqlite-vec | OSS | Shipped | HNSW via virtual table |
| pgvector | OSS | Shipped | IVFFlat / HNSW on PostgreSQL |
| s3-vectors | Cloud | Planned | AWS S3 Vectors (PRD #298) |
| qdrant | Cloud | Future | Dedicated vector DB |

### 3.4 GraphStore (extracted from PrimaryStore)

The Context Graph is currently co-located in PrimaryStore as the `memora_edges` table with recursive CTE traversal. Extraction enables dedicated graph backends for deployments with high fan-out or deep traversals.

```go
type GraphStore interface {
    Open(ctx context.Context, cfg GraphConfig) error
    Close() error
    Ping(ctx context.Context) error
    Capabilities() GraphCapabilities

    // Edge CRUD
    Link(ctx context.Context, edge types.Edge) (types.Edge, error)
    Unlink(ctx context.Context, edgeID, agentID string) error
    LinkBatch(ctx context.Context, edges []types.Edge) ([]LinkResult, error)
    CascadeForget(ctx context.Context, memoryID, agentID string) (int, error)

    // Traversal
    Neighbors(ctx context.Context, workspaceID, memoryID string, opts NeighborsOpts) ([]types.Edge, []types.MemoryHeader, error)
    Traverse(ctx context.Context, workspaceID, seedMemoryID string, opts TraverseOpts) (TraverseResult, error)
    Stats(ctx context.Context, workspaceID string) (nodeCount int, edgeCountByType map[string]int, err error)
}

type GraphCapabilities struct {
    MaxDepth         int  `json:"max_depth"`
    MaxNeighborsK    int  `json:"max_neighbors_k"`
    MaxLinkBatchSize int  `json:"max_link_batch_size"`
    SupportsWeighted bool `json:"supports_weighted"`
    SupportsLabeled  bool `json:"supports_labeled"`
}
```

**Candidate drivers:**

| Driver | Edition | Status | Notes |
|--------|---------|--------|-------|
| sqlite-graph | OSS | Current (in PrimaryStore) | Recursive CTE, depth ≤ 3 |
| postgres-graph | OSS | Current (in PrimaryStore) | Recursive CTE, ltree extension |
| neo4j | Cloud | Future | Native graph; Cypher queries |
| kuzu | OSS | Future | Embedded graph DB (no network dep) |
| apache-age | OSS | Future | PostgreSQL extension, Cypher |

**Note on MemoryHeader joins:** `Neighbors` and `Traverse` return `types.MemoryHeader`, which requires a join against MetadataStore. Two approaches:
1. GraphStore accepts a MetadataStore reference and performs the join internally.
2. GraphStore returns only edge data + memory IDs; the service layer resolves headers.

Recommendation: option 2 (cleaner separation, at the cost of one extra round-trip that can be batched).

### 3.5 LedgerStore (unchanged)

The existing `LedgerStore` interface is already cleanly separated. No structural changes needed.

---

## 4. Migration Plan

### 4.1 Phased Rollout

| Phase | Version | Deliverable |
|-------|---------|-------------|
| **Phase 0** | v0.1 | Ship the 3-adapter design as-is. Do NOT block release on adapter restructure. |
| **Phase 1** | v0.3 | ContentStore introduced as additive 4th adapter. Dual-write: content goes to ContentStore AND legacy `content TEXT` column. Reads still from PrimaryStore. **Already shipped (F13.T1).** |
| **Phase 2** | v0.5 | Read-path migration: Recall/Lookup prefer ContentStore when configured. MetadataStore interface extracted (initially backed by same PrimaryStore implementation). GraphStore interface extracted (initially backed by same PrimaryStore implementation). No data migration needed — same underlying tables, just different interface surface. |
| **Phase 3** | v0.7 | Data migration tooling: `memora-core migrate content` CLI command that backfills ContentStore from legacy `content TEXT`. After backfill + verification, the legacy column can be NULLed. |
| **Phase 4** | v1.0 | Remove legacy paths. `content TEXT` column dropped (or made nullable/deprecated). GraphStore and MetadataStore are separate binaries for Cloud deployments. Five-adapter shape is the only supported configuration. |

### 4.2 Dual-Write Window (Content)

During Phase 1–3:
```
Write path:  ContentStore.Put → MetadataStore.Update (content column)
Read path:   ContentStore.Get ?? MetadataStore.GetMemory().Content (fallback)
```

The `??` operator means: try ContentStore first; if ErrNotFound (pre-migration data), fall back to legacy. This is safe because:
- New writes always go to both.
- Old data that hasn't been migrated is only in the legacy column.
- The migration tool (`memora-core migrate content`) copies old → new and flips a per-memory flag.

### 4.3 Graph Extraction

The graph extraction is simpler because no data moves between storage systems in the OSS edition — the same SQLite file backs both MetadataStore and GraphStore. The extraction is purely an interface split:

1. Define `GraphStore` interface (Phase 2).
2. Implement `sqliteGraphStore` that opens the same `memora.db` and operates on `memora_edges`.
3. Service layer calls `GraphStore.Link(...)` instead of `PrimaryStore.GraphLink(...)`.
4. For deployers who want a dedicated graph backend (Phase 4), they configure `--graph-driver=neo4j` and a data-export tool copies edges.

### 4.4 Rollback Strategy

Each phase is designed to be independently reversible:
- Phase 1 rollback: stop writing to ContentStore; legacy column was never stopped, so no data loss.
- Phase 2 rollback: revert read-path to PrimaryStore; ContentStore data is still there for re-attempt.
- Phase 3 rollback: re-populate legacy column from ContentStore (exact reverse of the migration tool).

---

## 5. Service Layer Changes

The `internal/service/Service` struct currently holds:
```go
type Service struct {
    Primary  adapter.PrimaryStore
    Vector   adapter.VectorStore
    Ledger   adapter.LedgerStore
    Content  adapter.ContentStore  // added in v0.3
    Embedder embedding.Provider
    Identity map[string]adapter.IdentityProvider
    Pool     *embedqueue.Pool
}
```

At v0.5 (Phase 2), this becomes:
```go
type Service struct {
    Metadata adapter.MetadataStore  // was Primary (subset)
    Content  adapter.ContentStore
    Vector   adapter.VectorStore
    Graph    adapter.GraphStore      // was Primary (subset)
    Ledger   adapter.LedgerStore
    Embedder embedding.Provider
    Identity map[string]adapter.IdentityProvider
    Pool     *embedqueue.Pool
}
```

The PrimaryStore interface is deprecated but retained as a type alias wrapping MetadataStore + GraphStore for backward compatibility with existing drivers.

---

## 6. Security and Compliance

| Concern | Impact |
|---------|--------|
| **Encryption at rest** | ContentStore drivers (S3, file) can enforce AES-256 independently of metadata encryption. |
| **GDPR Art. 17 erasure** | `ContentStore.DeleteAllForMemory` + `GraphStore.CascadeForget` + `MetadataStore.ForgetMemory` — each adapter handles its own cascade. |
| **Tenant isolation** | All interfaces are workspace-scoped. Multi-tenant Cloud deployments can route workspaces to different physical stores. |
| **Audit trail** | LedgerStore appends are immutable. The split doesn't change the audit surface. |
| **Access control** | S3 bucket policies / IAM roles provide content-level access control that the monolithic SQLite file cannot. |

---

## 7. Performance Considerations

| Scenario | Current (monolithic) | Proposed (split) |
|----------|---------------------|-----------------|
| Imprint 1MB Memory | 1 SQLite write (serialized) | ContentStore write (parallel with MetadataStore) |
| Recall (hybrid search) | Vector query + FTS5 + join | Vector query (VectorStore) + keyword (MetadataStore) + content fetch (ContentStore) |
| Graph traversal depth=3 | Recursive CTE in PrimaryStore | Dedicated GraphStore (potentially in-memory for hot paths) |
| Forget cascade | Single transaction | Distributed: Metadata + Content + Graph + Vector deletes |

**Trade-off:** The split introduces coordination overhead (multiple adapter calls per operation). For the SQLite OSS edition where all adapters share the same file, this is negligible. For distributed Cloud deployments, the service layer must handle partial failures (write to Content succeeds, write to Metadata fails). Mitigation: idempotent Put operations + eventual consistency reconciliation.

---

## 8. Non-Goals

- No code changes ship with this document. This is a planning artifact.
- No commitment to specific driver implementations (e.g., Neo4j vs Kuzu vs Apache AGE).
- No wire-API changes; the five-adapter split is internal to memora, transparent to agents.
- No v0.1 release-blocker implications. The current 3-adapter design ships as-is.
- No new feature or work-item creation. This document enumerates options; human triage decides what ships.

---

## 9. Open Design Questions

1. **Should Cell.Text move entirely to ContentStore?** Currently cells have `text TEXT NOT NULL` in the metadata table (used by FTS5). If moved, FTS5 needs a separate denormalized index or a trigger-based sync.

2. **Does content-addressing (MD5/SHA) become a substrate requirement?** The ContentStore interface accepts `contentMD5` but doesn't enforce dedup. Making it enforced enables dedup across memories that share content (e.g., append-only logs with common prefixes).

3. **LedgerStore: keep separate or fold into MetadataStore?** Arguments for keeping: different access patterns (append-only vs CRUD), different retention policies, potentially different physical stores (event bus vs RDBMS). Arguments for folding: one fewer adapter to configure, simpler operations for small deployments.

4. **GraphStore Neighbors: who resolves MemoryHeaders?** Option A: GraphStore joins against MetadataStore internally (requires a reference). Option B: GraphStore returns edge data + IDs only; service layer batch-fetches headers from MetadataStore.

5. **FTS ownership.** Does keyword search belong to MetadataStore (current: FTS5 mirrors cell text) or ContentStore (co-located with the source of truth for text)?

---

## 10. Recommendation

Ship the five-adapter architecture incrementally per the phased plan in §4:
- **v0.1** (NOW): no changes. Ship the 3-adapter design.
- **v0.3** (DONE): ContentStore added as additive adapter, dual-write.
- **v0.5** (NEXT): Extract MetadataStore + GraphStore interfaces. Same underlying drivers, different interface surface. Read-path prefers ContentStore.
- **v0.7**: Data migration tooling. Legacy column nullable.
- **v1.0**: Five-adapter is the only shape. Legacy paths removed.

This sequencing minimizes blast radius per release while maintaining full backward compatibility throughout.
