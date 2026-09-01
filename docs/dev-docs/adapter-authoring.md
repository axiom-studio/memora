# Authoring a Memora adapter

Memora has five pluggable persistence contracts plus an identity
contract — each defined in [`pkg/adapter`](../../pkg/adapter):

- `MetadataStore` — tabular data (workspaces, collections, memories, cells, agents, watermarks).
- `ContentStore` — content bytes (memory and cell bodies).
- `GraphStore` — context graph (edges, traversal, statistics).
- `VectorStore` — embedding index.
- `LedgerStore` — append-only audit log.
- `IdentityProvider` — verifies an `agent_id` matches an `identity_proof`.

Third-party adapters import only `pkg/adapter` and `pkg/types` —
they do not need to depend on any `internal/` package.

## Hello-world VectorStore

```go
// In your-org/memora-acme-vec/vec.go
package vec

import (
    "context"
    "github.com/axiom-studio/memora/pkg/adapter"
)

func init() {
    adapter.RegisterVector("acme", func() adapter.VectorStore { return &Store{} })
}

type Store struct { /* ... */ }

func (s *Store) Open(ctx context.Context, cfg adapter.VectorConfig) error { /* ... */ return nil }
func (s *Store) Close() error                                              { return nil }
func (s *Store) Ping(ctx context.Context) error                            { return nil }
func (s *Store) Capabilities() adapter.VectorCapabilities {
    return adapter.VectorCapabilities{SupportsANN: true, MaxDimensions: 4096}
}
func (s *Store) PutVector(ctx context.Context, p adapter.VectorPut) error          { /* ... */ return nil }
func (s *Store) PutVectorsBatch(ctx context.Context, puts []adapter.VectorPut) error { /* ... */ return nil }
func (s *Store) Query(ctx context.Context, q adapter.VectorQuery) ([]adapter.VectorHit, error) {
    /* ... */
    return nil, nil
}
func (s *Store) DeleteVectors(ctx context.Context, keys []adapter.VectorKey) error { /* ... */ return nil }
```

Then in your `memora-core` build, add a blank import:

```go
import _ "github.com/your-org/memora-acme-vec"
```

…and the deployer can select your driver with
`--vector-driver=acme` or `MEMORA_VECTOR_DRIVER=acme`.

## Capability advertisement

The server checks `Capabilities()` at request-handling time and
returns a friendly capability error (HTTP 501 / MCP error -32603)
rather than attempting an unsupported operation. Examples:

- `VectorCapabilities.SupportsHybridFilter=false` — Recall hybrid
  mode falls back to keyword-only ranking and adds an
  `X-Memora-Hybrid-Degraded: true` response header.
- `LedgerCapabilities.SupportsQuery=false` — `GET /v1/.../ledger`
  returns 501 with `capability_unavailable`.

Always answer truthfully. The server's behavior degrades gracefully
when capabilities are missing, but it relies on your honesty.

## Schema migrations

Adapters own their migration lifecycle. The SQLite adapter embeds
its SQL files via `//go:embed migrations/*.sql` and runs them
idempotently at `Open()` time, tracking applied versions in
`memora_schema_migrations`. Postgres / etc. adapters follow the same
pattern.

## Watermark semantics

Every write must produce a fresh `wmk_<ULID>` watermark and append to
`memora_watermark_history`. The CAS contract is:

```go
newWmk, err := store.UpdateMemory(ctx, memID, expectedWmk, mem)
// if expectedWmk != head: return ErrCAS + current head_watermark
```

Return `types.ErrCAS` on conflict — the HTTP layer maps that to 412.

## Closed edge_type enum

Edge types are a closed enum (`pkg/types/edge.go`). Don't accept
unknown types — `types.ValidEdgeType(s)` is the canonical check.
Custom edge metadata goes in `Edge.PropertiesJSON`.

## Testing your adapter

The compliance test suite (`pkg/adapter/compliance/`) is the canonical
acceptance bar. v0.1 ships a minimal contract suite; the full suite
with the Context Graph block lands in v0.5 per the OSS roadmap.

In the meantime, here's the minimum you should test:

- Roundtrip per write verb (Imprint/Update/Patch/Append/Forget).
- CAS conflict — `UpdateMemory` with stale watermark returns `ErrCAS`.
- Unique-live-triple — `GraphLink` of the same `(source, target, edge_type)`
  twice returns `ErrAlreadyExists`.
- Cascade Forget — `Forget(memory)` followed by `Neighbors(other)` does
  not return the forgotten memory.
- Capability advertisement — every flag you advertise as `true`
  actually works; every flag advertised as `false` actually fails
  cleanly.

## Build tags

If your adapter only makes sense in a commercial/cloud deployment,
gate the registration with the `cloud` build tag:

```go
//go:build cloud

package vec
```

OSS builds (`make memora-oss`) won't include the package; Cloud builds
(`make memora-cloud`, or `go build -tags cloud`) will.
