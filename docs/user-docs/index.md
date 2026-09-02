# Memora Documentation

Memora is a memory service for AI agents: durable, versioned, multi-tenant storage with vector, keyword, and graph recall, reachable over REST, MCP, a command-line client, or a Go SDK. It ships as a single Go binary with SQLite defaults, so a working instance needs no external dependencies. Use these docs when you are running a Memora instance or writing an agent that stores and recalls memory through one.

## What is Memora?

Memora stores what an agent needs to remember and gives it back on demand. A Memory is a versioned document with content, tags, an owning agent, and an audit trail. Vector embeddings are an index over those documents, not the storage model, so a Memory keeps its identity, history, and relationships regardless of how it is searched.

The unit of embedding is smaller than the unit of storage. Every Memory is split into Cells by a chunker, and embeddings live on Cells. Editing one paragraph of a large document re-embeds one Cell rather than the whole document, and the response reports exactly how many Cells were re-embedded and how many were reused.

### Key Capabilities

- **Versioned writes with optimistic concurrency** — Every Memory carries a watermark. Updates, patches, and appends supply the watermark they expect, and a mismatch is rejected rather than silently overwriting a concurrent write.
- **Selective re-embedding** — Patch applies find-and-replace operations, re-chunks the result, and calls the embedding provider only for Cells whose text actually changed.
- **A typed context graph** — Memories are nodes and typed directed edges between them are first-class, queryable, and foldable into recall results.
- **Four recall modes** — Look up by ID, match keywords, search vectors, or combine keyword and vector results by weighted score.
- **Agent-attributed writes** — Every Memory, Cell, Edge, and audit record carries the agent that produced it, checked by a pluggable identity provider before the write proceeds.
- **Append-only audit** — Every mutation writes a ledger entry recording the operation, the actor, the before and after watermarks, elapsed time, and operation-specific counters.
- **Pluggable storage** — Five adapter seams — metadata, content, vector, graph, and ledger — each selected by driver name and independently swappable.
- **Four access surfaces** — A REST API, an MCP server, a CLI, and a Go SDK, all over one service layer, so no surface can drift from another.

## How It Works

1. **Create a workspace** — The workspace is the tenant boundary. It carries the default chunker, the embedding model, and the auto-linking policy for everything inside it.
2. **Imprint content** — The chunker splits the text into Cells, and content, metadata, and embeddings are written in that order.
3. **Embed** — Each Cell's text goes to the configured embedding provider and the resulting vector is written to the vector store, keyed by workspace and Cell.
4. **Auto-link** — When the workspace enables it, Memories similar enough to the new one receive `vector_neighbor` edges, up to the configured per-write limit.
5. **Recall** — A query runs keyword search, vector search, or both, and can then expand along graph edges from the seed hits, scoring each extra hop lower than the last.
6. **Audit** — Every one of the above appends a ledger entry, giving per-operation timing and counters without a separate metrics pipeline.

## Documentation Sections

| Section | Description |
|---|---|
| [Getting Started](getting-started.md) | Build the binaries, start a server, and run a first imprint, recall, and patch. |
| [Concepts](concepts.md) | The stored objects, the context graph, how recall ranks results, and what patch mode does. |
| [Configuration](configuration.md) | Every configuration key, its type and default, and how the file, environment, and flag layers resolve. |
| [Adapters](adapters.md) | The five storage seams and their drivers, the embedding providers, and the chunkers. |
| [CLI Reference](cli.md) | Every `memora-cli` command, its flags, output formats, and exit codes. |
| [API Reference](api.md) | The REST endpoints, the MCP tools, the Go SDK, and the full error-code list. |
| [Operations](operations.md) | Deployment, authentication, agent identity, observability, the web console, and content migration. |
