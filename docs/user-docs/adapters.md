# Adapters

Memora's storage is split into five independent seams, each selected by a driver name and configured with its own DSN. This page covers those seams and the two other pluggable pieces a workspace depends on: the embedding provider and the chunker.

## The Five Storage Seams

| Section | Description |
|---|---|
| MetadataStore | Workspaces, collections, memories, cells, tags, agents, pins, and watermarks. Drivers: `sqlite`, `postgres`. |
| ContentStore | Memory and Cell bodies, addressed by content hash. Optional. Drivers: `sqlite`, `postgres`, `file`. |
| VectorStore | Cell embeddings, keyed by workspace and Cell. Drivers: `sqlite-vec`, `pgvector`. |
| GraphStore | Edges, neighbours, traversal, and stats. Driver: `sqlite_graph`. |
| LedgerStore | The append-only audit log. Drivers: `sqlite`, `postgres`, `file`. |

Splitting storage this way means the scaling characteristics of one concern do not dictate the others. A deployment can put metadata and vectors on Postgres for concurrency while keeping the audit log on the local filesystem, without either choice touching the code that reads or writes memories.

Three of the seams are required. The other two behave differently when absent, and the difference matters:

- **Without a ContentStore**, the server stores metadata only. This is the default, and it is a working configuration rather than a degraded one.
- **Without a GraphStore**, every graph operation returns `501 capability_unavailable` — link, unlink, neighbours, traverse, and stats — and auto-linking cannot run. Recall silently skips graph expansion rather than reporting an error, so a recall request asking for expansion returns seed hits only and looks successful.

> **Only SQLite provides a GraphStore.** `sqlite_graph` is the sole registered graph driver, so a deployment that moves metadata, vectors, and the ledger to Postgres still needs a SQLite file for the graph. Plan for that file's durability alongside the Postgres backups.

Each LedgerStore driver declares its own capabilities, and querying and redaction are both optional. A ledger that cannot answer queries returns `501 capability_unavailable` from the audit endpoint while still accepting writes, so the audit trail stays intact even when it cannot be read back through the API.

## Write Ordering

The order in which a write touches the seams is fixed, and chosen for which failure is least damaging rather than which is fastest.

```text
imprint:  content → metadata → embeddings → ledger
forget:   metadata → content → vectors → ledger
```

On imprint, content is written first. If the metadata write then fails, the content is left orphaned and unreferenced — invisible, reclaimable, and harmless. The opposite order would leave a Memory that appears in listings but has no body, which is a far worse state to debug.

Forget reverses the sequence for the same reason. Metadata goes first so the Memory disappears from every read path immediately, and the content, vectors, and ledger entry follow.

## Choosing Drivers

The defaults put four seams in one SQLite file and leave the content store off:

| Field | Value |
|---|---|
| MetadataStore | `sqlite` at `<data_dir>/memora.db` |
| VectorStore | `sqlite-vec` at `<data_dir>/memora.db` |
| GraphStore | `sqlite_graph` at `<data_dir>/memora.db` |
| LedgerStore | `sqlite` at `<data_dir>/memora.db` |
| ContentStore | disabled |

This is the zero-dependency configuration: one file, no server to provision, and no network in the write path. It suits single-node deployments and local development, and it is what a fresh `memora-core serve` uses.

Moving to Postgres is a per-seam decision. `postgres` covers metadata, content, and the ledger, and `pgvector` covers vectors — but the graph stays on SQLite regardless. The `file` driver is available for the content store and the ledger, which is useful when bodies or audit records should sit on a mounted volume rather than inside a database.

See [Configuration](configuration.md) for the keys that set each driver and DSN.

## Embedding Providers

The provider is chosen by a `prefix:model` string, set globally through `embedding.model` or per workspace on the Workspace record.

| Section | Description |
|---|---|
| `noop` | Stub provider returning meaningless vectors. The default. |
| `local` | A locally hosted embedding model. |
| `openai` | OpenAI embedding endpoints. |
| `voyage` | Voyage AI. |
| `gemini` | Google Gemini. |
| `axiomstudio` | Axiom Studio hosted embeddings. |

> **The default is `noop:default`.** Out of the box, vector search runs against stub embeddings, so `vector` and `hybrid` recall return results with no semantic meaning. This is a deliberate default — it keeps a fresh instance from requiring an API key or a downloaded model — but it means recall quality cannot be judged until `embedding.model` or `MEMORA_EMBEDDING_MODEL` names a real provider.

Changing the embedding model does not re-embed existing Cells. Each Cell records the model that produced its vector, so a workspace whose model changes will hold vectors from both until its content is rewritten.

## Chunkers

The chunker splits a Memory's content into Cells. It is set per workspace, can be overridden per imprint, and some chunkers accept per-request configuration.

| Section | Description |
|---|---|
| `default` | General-purpose text splitting. |
| `markdown` | Splits on heading structure. |
| `no-chunk` | One Cell per Memory. |
| `code-go` | Go source. |
| `csv` | Row-oriented. Configurable per request. |
| `jsonl` | One record per line. Configurable per request. |

Chunker choice determines how much work an edit costs. Because embeddings live on Cells and patch re-embeds only the Cells whose text changed, a chunker that produces many small Cells makes edits cheap, and `no-chunk` removes the selective re-embedding saving entirely by making the whole Memory one Cell.

### Configuring the CSV Chunker

| Field | Value |
|---|---|
| `rows_per_cell` | Data rows per Cell. Integer of 1 or more. Default `1`. |
| `has_header` | Treat the first row as a header and prefix it to each Cell. Boolean. Default `true`. |
| `delimiter` | Field separator. A single character. Default `,`. |

### Configuring the JSON-Lines Chunker

| Field | Value |
|---|---|
| `lines_per_cell` | Non-blank lines per Cell. Integer of 1 or more. Default `1`. |
| `validate` | Reject the imprint when a line is not valid JSON. Boolean. Default `true`. |

Both are set with repeatable `--chunker-opt key=value` arguments on the CLI, or with the `chunker_config` object on an API or MCP imprint request:

```bash
memora-cli imprint --from-file export.csv \
    --chunker csv \
    --chunker-opt rows_per_cell=25 \
    --chunker-opt has_header=true
```

An option value of the wrong type is rejected at request time with a message naming the offending key, rather than being silently coerced.

> **Only imprint takes a per-request chunker.** Update, patch, and append re-chunk using the workspace's configured chunker, not one supplied on the request. A workspace whose default chunker does not suit its content should have that default changed rather than being corrected per write.
