# Concepts

Memora stores versioned documents, embeds them a chunk at a time, and links them into a typed graph. This page defines the objects those three ideas rest on and explains how recall and patch use them.

## Stored Objects

| Property | Description |
|---|---|
| Workspace | The top-level container and tenant boundary. Owns the default chunker, the embedding model, and the auto-link policy. ID prefix `ws_`. |
| Collection | An optional logical grouping inside a Workspace, usable as a recall filter. ID prefix `coll_`. |
| Memory | The unit of stored content: text plus tags, watermarks, agent attribution, and a content MD5. Also a node in the context graph. ID prefix `mem_`. |
| Cell | An addressable chunk of a Memory produced by the chunker. Cells are the unit of embedding. ID prefix `cell_`. |
| Edge | A typed, directed link between two Memories in one Workspace. ID prefix `edg_`. |
| Watermark | The version token on a Memory. Supplied as `If-Match` or `expected_watermark` to make a write conditional. Prefix `wmk_`. |
| Tag | A key/value attribute on a Memory, filterable in recall. |
| Pin | A saved recall query: text, mode, `k`, and an optional watermark that holds results at a point in time. ID prefix `pin_`. |
| Agent | A registered writer identity with a provider, an optional proof, a type, a model, and capabilities. ID prefix `agent_`. |
| Ledger entry | One append-only audit record per mutation. ID prefix `lg_`. |

Every identifier is a prefixed ULID, so an ID in a log line identifies its own type without a lookup. Cross-table references are checked against the expected prefix on write, which turns a mismatched ID into an immediate error rather than a dangling row.

## Memories and Cells

A Memory holds the whole document. Cells hold slices of it, in order, each with its own text and an MD5 of that text.

```text
Memory (mem_…)  ──chunker──>  Cell 0 (cell_…)  ──embed──>  vector
                              Cell 1 (cell_…)  ──embed──>  vector
                              Cell 2 (cell_…)  ──embed──>  vector
```

The split matters because embedding is the expensive part of a write. Storing the document whole keeps its identity, tags, watermark history, and edges stable; embedding it in pieces means an edit only pays for the pieces it touched. Which chunker performs the split is a workspace setting, overridable per imprint — see [Adapters](adapters.md) for the available chunkers.

Each Memory carries two watermarks: `created_watermark` from its first write and `head_watermark` for its current version. Reading a Memory at a past watermark is a distinct request, and watermark history is retained for a fixed seven-day window.

## Watermarks and Concurrency

Every write that modifies existing content — update, patch, and append — requires the watermark the caller believes is current. Memora compares it against the Memory's head and rejects the write if they differ.

```text
read head_watermark → modify → write with expected watermark → head advances
                                            │
                                            └─ mismatch → 412 cas_conflict, nothing written
```

A conflict means another writer committed between the read and the write. Nothing is merged and nothing is overwritten; the correct response is to re-read the Memory and decide what to do with the newer content. This is the same guarantee for two agents racing on one Memory as for a human and an agent racing on one document.

Imprint takes no watermark, because it creates a Memory that has no prior version.

## Patch Mode

Patch applies a list of find-and-replace operations to a Memory's content under a watermark check, re-chunks the result, and re-embeds selectively.

```text
PATCH + expected watermark
  → watermark matches?  no ─> 412 cas_conflict
                        yes
  → apply ops to content
  → anchor missing?     yes ─> 400 patch_anchor_not_found
                        no
  → re-chunk into new Cells
  → per Cell: text hash unchanged? ─ yes ─> reuse existing vector (skipped)
                                   └ no  ─> call embedding provider (re-embedded)
  → write ledger entry with the counters
```

Each new Cell is matched against an old one — by Cell ID where the chunker preserved it, otherwise by position — and if the matched pair have identical text hashes and the old Cell already has a vector, the new Cell inherits that vector and the embedding provider is never called for it.

The response reports `cells_re_embedded`, `cells_skipped`, `cells_added`, and `cells_removed`, and the same counters go into the ledger entry, so the saving is measurable per write rather than merely claimed. When the new content produces fewer Cells than before, the leftover vectors are deleted.

Two failures are specific to patch. A watermark mismatch returns `412 cas_conflict`, as with any conditional write. An `old_string` that does not appear in the current content returns `400 patch_anchor_not_found`, and no operation in the batch is applied.

## The Context Graph

Edges are declared by the caller and typed against a closed enum. There is no free-form edge label; adding a type requires a Memora release.

| Status | Description |
|---|---|
| `parent_of` | Hierarchical containment. |
| `derived_from` | This Memory was produced from another by summarization, split, refinement, or import. |
| `supersedes` | This Memory replaces an older one. |
| `references` | A soft curated link — the default "these two are related" type. |
| `session_of` | The Memory was written inside a named session anchor, which is itself a Memory. |
| `mentions` | The source's content refers to the target's ID. |
| `vector_neighbor` | Written by the system from cell-level vector similarity at write time. Not a user assertion. |

```text
Session anchor ──session_of──> Note ──references──> Summary
Source document ──parent_of──> Section ──derived_from──> Summary ──supersedes──> Old summary
Note ┈┈vector_neighbor┈┈> Section        (dashed: written by auto-linking, not asserted)
```

The first six types are assertions a caller makes about meaning. The seventh is an observation the system makes about similarity, and Memora keeps the distinction enforced rather than conventional: `vector_neighbor` edges cannot be removed with unlink, which returns `cannot_unlink_synthetic_edge`. Self-loops are rejected, and asserting a link between two Memories already joined by a synthetic edge returns `already_linked_via_synthetic`.

Three graph operations are available. **Neighbors** returns one hop, filtered by direction and edge type. **Traverse** walks breadth-first to a given depth and returns layers, each hit carrying the edge it arrived through; depth is capped at `3`, and asking for more returns `graph_traverse_depth_exceeded`. **Stats** returns the node count and the edge count by type.

Deleting a Memory cascades to its incident edges, and the response reports how many were removed.

### Auto-Linking

When a Workspace enables it, a new Memory is compared against existing ones at write time and similar Memories receive `vector_neighbor` edges automatically.

| Field | Value |
|---|---|
| Enabled | Off unless `auto_link_enabled` is set on the Workspace. |
| Similarity threshold | `0.7`, clamped to a maximum of `1.0`. |
| Edges per write | `10` by default, hard-capped at `50`. |
| Incoming links per target | `100` per target Memory per day. |
| Per-imprint override | `auto_link: false` on a single imprint request turns it off for that write. |

Auto-link edges are attributed to the reserved agent `agent_system_auto_link`, which keeps them distinguishable from anything a real agent asserted.

## Recall

Recall takes a query and returns ranked hits. Four modes are available; `hybrid` is the default and `k` defaults to `5`.

| Status | Description |
|---|---|
| `lookup` | Fetch specific Memories by ID. No scoring. |
| `keyword` | Case-insensitive substring match over the Workspace's Memories. |
| `vector` | Embed the query and run a nearest-neighbour search over Cell vectors. |
| `hybrid` | Run both and merge by weighted score. Default weights are `0.7` vector and `0.3` keyword. |

```text
query → mode ─ lookup  ─> fetch by ID ──┐
             ├ keyword ─> substring scan ┤
             ├ vector  ─> vector search  ├─> seed hits ─> graph expansion? ─ no ─> sort, truncate
             └ hybrid  ─> both, merged  ─┘                       │
                                                                yes
                                                                 ↓
                                              traverse from each seed, score each layer lower
```

Every hit is labelled `via: seed` or `via: graph`. Graph-expanded hits carry provenance naming the seed Memory, the edge traversed, its type, and the layer it was found at.

Expansion scores a hit as `weight × decay^layer × seed score`, with expansion weight defaulting to `0.2`, the per-layer decay fixed at `0.7`, and direction defaulting to `out`. A second-hop neighbour therefore scores well below a direct hit, which is the intent: graph expansion widens recall without letting distant relatives outrank actual matches. When expansion is on, results are truncated to `k` plus the requested maximum number of neighbours rather than to `k` alone.

Filters apply to both seeds and expansion: collection, writing agent (a single value, an `in` list, or a `not_in` list), creation-time range, and tag key/value.

> **Keyword search is a substring scan, not a full-text index.** It lists up to 500 Memories in the Workspace and filters them in memory. It does not rank by term frequency, and on a Workspace larger than 500 Memories it does not see past that window. Vector and hybrid recall are the modes that scale.

## The Audit Ledger

Every mutation appends a ledger entry. Entries are never modified, and the operations recorded are `imprint`, `update`, `patch`, `append`, `forget`, `link`, `unlink`, `graph_link`, `edge_cascade_forget`, `auto_link_skipped`, and `orphan_gc`.

Each entry carries the operation, its target, the acting agent, the before and after watermarks, a request ID, elapsed milliseconds, and operation-specific counters. Because the counters include the Cell-level re-embedding figures, querying the ledger over a time range yields write throughput, latency, and re-embedding efficiency directly. See [Operations](operations.md) for querying and redaction.
