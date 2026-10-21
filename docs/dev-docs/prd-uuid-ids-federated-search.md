# PRD: UUIDv4 identifiers and federated memory search

**Status:** Accepted — tracked as features F20 (UUIDv4 identifiers), F21 (node-wide search) and F22 (federated search)
**Source:** VibeFlow document #649 "memora-requirements-gaps.md" (2026-10-21)
**Baseline:** `main` at `e2c7c02` (2026-10-18)

The body below is the source document unchanged. [Verification notes](#verification-notes) and the [ticket map](#ticket-map) at the end were added when it was filed.

---

# Memora: requirements and gaps

**Scope:** two requirements for Memora, the current state of `axiom-studio/memora` at `main` `e2c7c02` (2026-10-18), and the gaps to implement.

| # | Requirement |
|---|---|
| **R1** | Every identifier is a UUIDv4. |
| **R2** | Federated memory search: a node sends a prompt or keyword query to its peers. Each peer searches its memories with keyword and vector (probabilistic) matching and returns the relevant memories with their data. The caller merges the results. |

**Not in scope:** workspace-level federation. That means no workspace grants, no workspace ID or name mapping between nodes, no shared workspaces across nodes, and no writes to peers. A connected peer searches its whole node. Finer exposure control can come later if it is ever needed.

---

## R1: All IDs are UUIDv4

### Requirement

- Every ID is a canonical lowercase UUIDv4 string such as `3f2b8c1e-6d4a-4f0b-9a7e-2c5d8e1f4a90`. No type prefixes and no ULIDs.
- This applies to workspaces, collections, memories, cells, edges, watermarks, ledger entries, pins, snapshots, agents, request IDs, the node ID and peer IDs.
- Human-readable names such as `market`, `outbox` and `Market Agent` are display fields, never keys.
- APIs reject any ID that is not a valid UUID, with a 400 error.

### Current state

- **IDs are prefixed ULIDs.**
  - One generator, `pkg/types/ids.go:35` `NewID(prefix)`, with 44 non-test call sites.
  - Prefixes: `ws_ coll_ mem_ cell_ edg_ agent_ wmk_ lg_ pin_ snap_ fed_ req_`.
- **Prefixes are enforced** by 14 `MustHavePrefix` calls, plus `HasPrefix` checks for `agent_` (UI) and `fed_` (config).
- **Agent IDs are supplied by the client and must start with `agent_`.**
  - The `anthropic_session` provider derives a deterministic `agent_anthropic_session_<hash>` ID (`pkg/identity/identity.go:65`).
  - Two sentinel agents (`agent_legacy_vibeflow`, `agent_system_auto_link`) are seeded into every workspace.
- **Synthetic edge IDs** are built in SQL as `'edg_synthetic_' || archive.id` and recognised by that prefix.
- **IDs are part of storage keys.** Vector keys equal cell IDs, and file-content paths are `{ws}/{mem}/{cell}.txt`.
- **The ledger relies on ID order.** It sorts by `ts DESC, ledger_id DESC`, but its cursor is `ledger_id < ?` alone. That only works because ULIDs sort by time.
- **Memory and workspace listing pages by `id >`.** With UUIDv4 this still works but is no longer chronological. Nothing reads timestamps out of IDs.
- **Schema:** all ID columns are unbounded `TEXT`, so no column-type change is required.
- **Tests and docs:** 667 prefixed ID literals across 39 test files, plus about 13 in docs.
- **Existing bug:** pins are minted with `NewID("pin")`, which produces `pin01H…` with no underscore. It goes away with R1.

### Gaps

| ID | Gap | What to implement |
|---|---|---|
| **ID-1** | ULID generator | Replace `NewID` and `NewWatermark` with `uuid.NewString()` (v4). Remove the prefix constants and the `oklog/ulid` dependency. |
| **ID-2** | Prefix validation | Replace `MustHavePrefix` and the `HasPrefix` checks with one `types.ParseID` that checks for a UUID, at the API, MCP, CLI and UI boundaries. Errors name the expected type, e.g. `memory_id must be a UUID`, since the ID no longer carries it. |
| **ID-3** | Agent IDs | Agent IDs are UUIDv4s and the name moves to `display_name`. Server-derived identities such as `anthropic_session` store their fingerprint in a new `identity_key` column; the first registration mints a UUIDv4 and later sessions resolve to it. The sentinel agents get fixed, published UUIDv4 constants. The CLI default (`agent_opaque_local`) and the UI placeholder (`agent_my_bot`) are replaced. |
| **ID-4** | Synthetic edges | Mint a UUIDv4 per synthetic edge and persist it. Mark synthetic edges with a boolean column, not an ID prefix. |
| **ID-5** | Ledger pagination | Change the cursor to the composite `(ts, ledger_id)` (`WHERE (ts, ledger_id) < (?, ?)`) in sqlite and postgres. Also update the UI audit page and the `since_ledger_id` API. |
| **ID-6** | Chronological listing (optional) | Memory and workspace listing pages by `id`, which becomes random order. Switch to `(created_at, id)` cursors wherever chronological order is expected (`next_cursor`, orphan GC and migrate are unaffected). |
| **ID-7** | Request IDs and node ID | `X-Request-ID` defaults to a UUIDv4. Replace `memora-core federation init` (`fed_<ULID>`) with a per-node `node_id` UUIDv4 (see R2). |
| **ID-8** | Existing data | A one-time migration command that maps every old ID to a new UUIDv4 and rewrites all references. That covers foreign keys, tags, edges, ledger targets, pins, vector-store keys and content-store file paths. Pre-1.0 deployments may start fresh instead, but the decision must be documented. |
| **ID-9** | Tests and docs | Regenerate fixtures (667 literals), update docs and examples, and add a test that rejects non-UUID IDs on every route and MCP tool. |

---

## R2: Federated memory search

### Requirement

```
agent ─► local node: POST /v1/search { query, mode: hybrid, k, federated: true }
          │ 1. search all local memories (keyword + vector)
          │ 2. send the same query TEXT to every connected peer
          ▼
        peer node: authenticates the peer, searches ALL of its memories node-wide,
                   embedding the query with its OWN model, returns top-k hits with data
          │
        local node: merge local + peer hits (rank fusion), dedupe, top k
          ▼
agent ◄─ hits: { memory_id, node_id, node_name, score, matched_text, content?, tags, written_by, created_at }
agent ─► GET /v1/memories/{id}?node={node_id}   fetch the full memory from the owning node
```

- **Query:** a prompt or keywords sent as text, in `keyword`, `vector` or `hybrid` mode. Vector search gives probabilistic matches.
- **Scope:** each peer searches every memory on its node. The caller does not name workspaces.
- **Embeddings:** each node embeds the query with its own model, so nodes may use different models and dimensions.
- **Results:** every hit carries the data (the matched text, and optionally the full content) plus where it came from.
- **Reliability:** per-peer timeouts and partial results. One slow or failed peer never fails the search.
- **Trust:** peer-level. A peer is allowed or not; nothing finer.
- **Audit:** both sides log searches, including the query text, which is the sensitive part.

### Current state

**Local search**, which everything below builds on:

- **Recall is one workspace per call.** `Recall(ctx, workspaceID, req)`, and both vector stores require a `workspace_id` filter. There is no node-wide search.
- **Keyword mode is not full-text search.**
  - It does a case-insensitive substring match of the whole query against only the 500 most recent memories, with an unnormalised score.
  - An FTS5 table (`memora_memories_fts`) is maintained on every write but never queried. Postgres has no full-text search.
- **Vector mode filters after ranking.** It takes top-k, then applies filters, so it can return fewer than k.
  - Each hit costs one `GetMemory` call.
  - The returned text is a 240-character excerpt of the memory, not the matched cell.
- **Hybrid mode fuses raw scores.**
  - The formula is `0.7·cosine + 0.3·keyword` on different, unnormalised scales.
  - When several cells of one memory match, the last one overwrites the others rather than the best one winning.
  - Errors from either side are swallowed.
- **Embeddings are effectively off by default.**
  - The default is `noop:default`, a 384-dimension byte histogram, so hybrid mode is 70% noise.
  - The Docker image has no local embedder: `local` exists only behind a build tag.
  - The `axiomstudio` provider is a stub that always fails, and recall quietly drops to keyword results.
  - Changing the model's dimension silently hides old vectors (sqlite-vec) or breaks every query (pgvector).
  - There is no re-embed command.
  - The node has a single embedder. The per-workspace `embedding_model` field is stored but never used.
- **`include_cells` is accepted and ignored.** Only lookup mode returns full content.
- **sqlite-vec uses brute-force cosine** with no ANN index. Fine for small nodes; pgvector has HNSW.

**Federation** (`internal/federation`, about 690 lines, not used anywhere):

- **Reusable:** `FanoutRecall` (parallel calls, per-peer timeout, partial failures, latency), `Aggregate` (per-peer z-score, dedupe, top-K), the 2-hop loop guard, per-peer rate limits, and the ledger op names.
- **Never used:** nothing outside the package imports it, and there is no inbound handler.
- **Peer authentication cannot work.**
  - The client sends `X-Memora-Federation-Auth`, but the server only accepts its single `Authorization` key, so peers get 401.
  - There is no inbound per-peer key check.
- **No node identity.** `X-Memora-Local-Instance-ID` is set to the shared `federation_id`.
  - The inbound peer lookup, which is keyed by peer name, always fails.
  - The loop guard would reject every call.
- **Fan-out is workspace-scoped:** `POST {peer}/v1/workspaces/{ws}/recall`.
- **Aggregation drops fields:** `partial_success`, `failed_peers`, `peer_latency_ms`, `federation_id` and `peer_id` are never filled in.
- **Peers come only from config files.** UI add, edit and remove peer return "requires config file changes".
- **Hardcoded limits:** rate limit 50 QPS and timeout 5 s.
- **mTLS is CA-only;** no client certificate is presented.

### Gaps: local search (needed before federation is worth doing)

| ID | Gap | What to implement |
|---|---|---|
| **S-1** | No node-wide search | Add `Service.Search(ctx, req)` across all workspaces (optional `workspace_ids` filter). Make the vector-store `workspace_id` filter optional, with a node-wide query in sqlite-vec and pgvector. Make keyword search node-wide. Expose it as `POST /v1/search` and the MCP tool `memora_search`. |
| **S-2** | Keyword search is substring-over-500 | Query the existing FTS5 table with `MATCH` and BM25 ranking in sqlite. Add `tsvector` + GIN with `ts_rank` in postgres. Tokenise the query. Remove the 500-memory cap. |
| **S-3** | Hybrid scoring is wrong | Fuse keyword and vector rankings with reciprocal rank fusion (or normalise both scores). Keep the best cell per memory. Report a degraded mode (`vector_unavailable`, `keyword_unavailable`) instead of swallowing errors. |
| **S-4** | Filters applied after top-k | Push agent, tag and time filters into the store queries, or overfetch and refill until k results. |
| **S-5** | Thin results | Return the matched cell text and its position. Add `include_content` for the full memory content, capped by `max_content_bytes`. Implement `include_cells`. Batch memory loads instead of one `GetMemory` per hit. |
| **S-6** | Vector search unusable by default | Require a real embedder for vector and hybrid modes; `noop` returns `vector_unavailable` and the server warns at start-up. Ship a local embedder in the Docker image (build with `local_embed`, or provide a variant image) so offline nodes work. Make the `axiomstudio` stub fail loudly. |
| **S-7** | Changing embedding models breaks search | Record the model and dimension per vector row (sqlite-vec already stores `dim`). Add a `memora-core reembed` command that re-embeds every cell when the model changes. Have pgvector detect a dimension mismatch at start-up and refuse to start, with a clear message. |

### Gaps: federated search

| ID | Gap | What to implement |
|---|---|---|
| **F-1** | No node identity | Add a `[node] id` UUIDv4, created on first start and persisted, and a `[node] name` label. Use the ID for `X-Memora-Local-Instance-ID`, peer registry keys, loop-guard paths and result attribution. Drop `federation_id`, or keep it as an optional UUIDv4 cluster label. |
| **F-2** | No inbound search endpoint | Add `POST /v1/federation/search`, called by peers, which runs `Service.Search` node-wide (S-1) and returns hits with data. Enforce the 2-hop loop guard and the inbound rate limit. The query arrives as text and the peer embeds it locally. |
| **F-3** | Peer authentication | Each peer has its own credential: an inbound key the peer presents in `X-Memora-Federation-Auth`, checked in constant time; mTLS with a client certificate is optional. Federation routes bypass the server's single API key, and normal routes keep it. Unknown or revoked peers get 401 or 403. |
| **F-4** | Outbound fan-out | `POST /v1/search` with `federated: true` (optional `peers: [node_id…]`, `per_peer_k`, `timeout_ms`) runs local search plus a parallel fan-out to `/v1/federation/search`. Adapt `FanoutRecall` to the node-wide endpoint. Make the per-peer timeout and rate limit configurable per peer. |
| **F-5** | Merging results | Merge local and peer hits with reciprocal rank fusion. Z-score normalisation (the existing `Aggregate`) is acceptable but more fragile across different models. Dedupe on `(node_id, memory_id)`, which R1 makes globally unique. Fill `node_id`, `node_name`, `via`, `partial_success`, `failed_peers` and `peer_latency_ms`. An optional `rerank: true` hook lets the caller re-score the merged top-N. |
| **F-6** | Fetching the full memory | Add `GET /v1/federation/memories/{memory_id}` (inbound, peer-authenticated) and a caller-side `GET /v1/memories/{id}?node={node_id}` that proxies to the owning node. Agents search, then fetch the whole memory. |
| **F-7** | Managing peers at runtime | Keep a registry and client pool that can change while the node runs, persisted in the metadata store. Add `GET/POST /v1/federation/peers` and `DELETE /v1/federation/peers/{node_id}` (body: `node_id`, `name`, `url`, `key`, timeouts). Wire the existing UI peer forms to them and show real peer IDs. Config-file peers become the seed only. |
| **F-8** | Audit and limits | Write `federation_search`, `federation_inbound`, `federation_rejected` and `federation_fetch` to the ledger on both sides, with the peer `node_id`, query text and hit count. Fill the ledger's `api_key_id`/peer column. Wire the inbound and outbound rate limiters. Add peer latency and failure metrics to `/metrics`. |
| **F-9** | Agent and CLI access | Add the MCP tool `memora_search` (`query, mode, k, federated, include_content`) and `memora_fetch` (`memory_id, node_id`). Add CLI `memora-cli search --federated`. Make results self-describing so an agent can cite the source node. |
| **F-10** | Tests | An end-to-end test with two nodes using different embedding models: a federated query returns tagged hits from the peer, an unknown or revoked peer is refused, a down peer yields `partial_success`, and a 3-node loop is stopped. Replace the CLI federation test that only checks the exit code. |

### What we are dropping from the existing design

Workspace-scoped fan-out (`/v1/workspaces/{ws}/recall` on peers), per-peer workspace allowlists (`Peer.Workspaces` and `IsAuthorizedPeer`), and the shared `federation_id` as identity. These are not needed for R2.

---

## Final gap list (implementation order)

| Phase | Items | Outcome |
|---|---|---|
| **1. IDs** | ID-1 to ID-9 | Every ID is a UUIDv4, ledger paging is correct, and existing data is migrated or reset. |
| **2. Local search** | S-1, S-2, S-3, S-5, S-6 | Node-wide `POST /v1/search` with real keyword search (FTS/BM25), working vector search, correct hybrid ranking, and hits that carry their data. |
| **3. Federation core** | F-1, F-2, F-3, F-4, F-5 | `federated: true` searches every connected peer by prompt or keyword and returns merged hits tagged with the source node. |
| **4. Operability** | F-6, F-7, F-8, F-9 | Fetch full memories from peers, connect and disconnect peers at runtime from the UI or API, audit trail, and MCP tools for agents. |
| **5. Hardening** | S-4, S-7, F-10 | Filters that don't lose results, safe embedding model changes, end-to-end federation tests. |

### Done when

1. No ID anywhere (API, MCP, CLI, UI, database, ledger, vector store, file paths) is anything but a UUIDv4. Non-UUID input is rejected.
2. On a node with a real embedder, `POST /v1/search {"query": "why did NVDA drop today?"}` returns memories from every workspace on that node, ranked by hybrid relevance, each with its matched text.
3. With two nodes using different embedding models, adding the peer at runtime and searching with `federated: true` returns hits from both. Peer hits carry the peer's `node_id`, and the full memory can be fetched by `(node_id, memory_id)`.
4. Removing the peer stops results immediately. A stopped peer yields `partial_success: true`. An unknown caller gets 401 or 403. All of it appears in the ledger on both nodes.
5. Agents can do all of the above through `memora_search` and `memora_fetch`.

---

## Verification notes

Checked against the code at `e2c7c02` when the tickets were filed. The source document stands; these notes correct counts and settle one ambiguity.

- **Counts.** A `grep` for `NewID(` finds 33 non-test call sites (the document says 44), and there are 16 non-test `MustHavePrefix` calls (it says 14). The scope is the same either way: every call site changes.
- **`internal/federation` is unused.** Confirmed: no other package imports it. `internal/config` and `internal/ui` have their own `federation` config section and fields.
- **No release yet.** The repository has no git tags, so no tagged release has shipped with prefixed IDs. That bears on ID-8 (migrate or start fresh), which the user still has to decide.
- **Which routes take peer credentials (F-3 and F-7).** "Federation routes bypass the server's single API key" applies only to the routes peers call: `POST /v1/federation/search` and `GET /v1/federation/memories/{memory_id}`. The peer-management routes (`GET/POST /v1/federation/peers`, `DELETE /v1/federation/peers/{node_id}`) are operator routes and keep the normal API key.
- **ID-1 and ID-2 ship together.** Changing the generator breaks every prefix check, so the two cannot land separately with tests passing.
- **ID-7 is split.** The request-ID half is in F20. The node-ID half is implemented under F-1.

## Ticket map

| Gap | VibeFlow todo | Depends on |
|---|---|---|
| This document | #4345 | — |
| **F20 — UUIDv4 identifiers (#851)** | | |
| ID-1 + ID-2 | #4346 | #4345 |
| ID-3 | #4347 | #4346 |
| ID-4 | #4348 | #4346 |
| ID-5 | #4349 | #4346 |
| ID-6 | #4350 | #4346 |
| ID-7 (request IDs) | #4351 | #4346 |
| ID-8 | #4352 | #4347, #4348, #4349 |
| ID-9 | #4353 | #4347–#4351 |
| **F21 — Node-wide search (#852)** | | |
| S-1 | #4354 | #4346 |
| S-2 | #4355 | #4354 |
| S-3 | #4356 | #4355 |
| S-5 | #4357 | #4354 |
| S-6 | #4358 | #4356 |
| S-4 | #4359 | #4355 |
| S-7 | #4360 | #4357, #4358 |
| **F22 — Federated search (#853)** | | |
| F-1 (+ ID-7 node ID) | #4361 | #4346 |
| F-3 | #4362 | #4361 |
| F-2 | #4363 | #4362, #4354, #4357 |
| F-4 | #4364 | #4363 |
| F-5 | #4365 | #4364, #4356 |
| F-6 | #4366 | #4363 |
| F-7 | #4367 | #4362 |
| F-8 | #4368 | #4365, #4366, #4367 |
| F-9 | #4369 | #4365, #4366 |
| F-10 | #4370 | #4368, #4369 |
