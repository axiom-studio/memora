# Getting Started

This page takes a Memora instance from nothing to a stored, recalled, and patched Memory. It assumes a Unix-like shell and, for the build path, Go 1.25 or newer.

## Installing Memora

Memora ships as two binaries built from one Go module: `memora-core` is the server, and `memora-cli` is the command-line client.

```bash
git clone https://github.com/axiom-studio/memora && cd memora
make build
```

That writes `bin/memora-core` and `bin/memora-cli`. Both are static, `CGO_ENABLED=0` builds with no runtime dependencies.

To run the server from a container instead of building it:

```bash
docker run -p 7777:7777 -v memora-data:/data \
    -e MEMORA_API_KEY=dev-key-change-me \
    ghcr.io/axiom-studio/memora-core:latest
```

The image is distroless, runs as a non-root user, exposes port 7777, and defaults to the `serve` subcommand with its data directory at `/data`. Mount a volume there, as above, or the stored data disappears with the container.

The API key is required, not optional. The image binds `:7777`, which is reachable beyond loopback, and the server refuses to start unauthenticated on such an address — so a `docker run` without `MEMORA_API_KEY` exits immediately.

## Starting the Server

```bash
./bin/memora-core serve --addr=127.0.0.1:7777 --data-dir=./data &
```

With the SQLite defaults this needs no other configuration: metadata, vectors, the graph, and the ledger all live in `./data/memora.db`.

> **Binding beyond loopback requires an API key.** With no key configured, `memora-core serve` starts on a loopback address but refuses to start on any other bind, exiting with an error rather than serving unauthenticated. Set `MEMORA_API_KEY`, or pass `--allow-no-auth` to override deliberately. See [Operations](operations.md) for the full authentication model.

Point the CLI at the server and create the first workspace, which is the tenant boundary every other call is scoped to:

```bash
export MEMORA_ENDPOINT=http://localhost:7777
export MEMORA_WORKSPACE=$(./bin/memora-cli workspaces create --name demo -o json | jq -r .id)
```

Workspace IDs are UUIDv4s, so `$MEMORA_WORKSPACE` now holds something like `3f2b8c1e-6d4a-4f0b-9a7e-2c5d8e1f4a90`.

## Storing the First Memory

```bash
./bin/memora-cli imprint --text "Customer wants the enterprise plan."
```

The response reports the new Memory's ID, its opening watermark, an MD5 of the content, how many Cells were created, and whether the Memory is ready to be recalled:

```json
{
  "id": "7d4e2a91-5b3c-4f8e-a1d6-9c0b2e7f3a58",
  "watermark": "b2c9e4f1-8a7d-4e3b-9f6c-1d5a0e8b7c42",
  "content_md5": "…",
  "cells_created": 1,
  "recall_ready": true,
  "written_by_agent_id": "c1a0e5d2-7b3f-4a6e-9d8c-1f2e3a4b5c6d",
  "ledger_id": "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d",
  "latency_ms": 4
}
```

Embedding runs inline as part of the write, which is why `recall_ready` is already true rather than pending. It also means a large imprint holds the request open for as long as the embedding provider takes.

The `written_by_agent_id` is `c1a0e5d2-7b3f-4a6e-9d8c-1f2e3a4b5c6d` because that is the CLI's default agent, shared by every local CLI user. Every write is attributed to some agent; override it with `--agent-id` or the `MEMORA_AGENT_ID` environment variable.

## Recalling It

```bash
./bin/memora-cli recall enterprise
```

Recall defaults to `hybrid` mode and returns the top 5 hits. Hybrid runs both a keyword search and a vector search and merges their rankings by reciprocal rank fusion, weighting the two equally by default.

> **The default embedding provider produces no vectors.** Out of the box the embedding model is `noop:default`, so hybrid recall answers from keywords alone (the response says `degraded: ["vector_unavailable"]`) and vector recall is unavailable. Set `hash:default` for offline lexical vectors, or a semantic provider, before judging recall quality. See [Adapters](adapters.md) for the available providers.

## Patching It

Patch is where Memora differs most from a plain document store. It applies find-and-replace operations under a watermark check, re-chunks the result, and only re-embeds the Cells whose text actually changed.

Read the current watermark, then supply it as the expected value:

```bash
WMK=$(./bin/memora-cli lookup 7d4e2a91-5b3c-4f8e-a1d6-9c0b2e7f3a58 -o json | jq -r .head_watermark)

./bin/memora-cli patch 7d4e2a91-5b3c-4f8e-a1d6-9c0b2e7f3a58 \
    --patch '[{"old_string":"enterprise","new_string":"premium"}]' \
    --if-match "$WMK"
```

The response reports how much embedding work the edit avoided:

```json
{
  "id": "7d4e2a91-5b3c-4f8e-a1d6-9c0b2e7f3a58",
  "watermark": "e6a1d3b8-2f4c-4a9e-8b7d-5c0f9e1a2d63",
  "patches_applied": 1,
  "cells_re_embedded": 1,
  "cells_skipped": 0,
  "last_modified_by_agent_id": "c1a0e5d2-7b3f-4a6e-9d8c-1f2e3a4b5c6d",
  "ledger_id": "5d6e7f80-9a1b-4c2d-ae3f-4b5c6d7e8f90",
  "latency_ms": 6
}
```

On a single-Cell Memory there is nothing to skip. The saving appears on larger documents, where changing one paragraph leaves every other Cell's text hash unchanged and those Cells keep their existing vectors.

Passing a watermark that no longer matches the Memory's head returns `412 cas_conflict` and changes nothing, which is the intended way to detect that another writer got there first. Omitting `--if-match` on a patch is rejected as well — patch, update, and append all require a watermark.

## Connecting an Agent Over MCP

To expose Memora to an MCP-aware agent, run the binary in stdio mode and point the client at that process:

```bash
./bin/memora-core mcp --data-dir=./data
```

This speaks JSON-RPC 2.0 over stdio and exposes tools covering workspaces, collections, memories, recall, the graph, and agents. It reads the same data directory as `serve`, so a stdio MCP session and an HTTP client can share one instance.

> **The stdio transport is unauthenticated unless you give it a key.** With no key configured it trusts its caller completely — it only talks to the process that started it — and logs a warning saying so. Passing `--api-key`, or setting `MEMORA_API_KEY`, makes the `initialize` handshake require that key and gates every other method behind it.

## Where to Go Next

| Section | Description |
|---|---|
| [Concepts](concepts.md) | What a Cell, a watermark, and an edge actually are, and how recall scores results. |
| [Configuration](configuration.md) | Moving off the defaults: storage drivers, a real embedding model, TLS. |
| [CLI Reference](cli.md) | The full command set, including the graph and audit commands not shown here. |
| [API Reference](api.md) | Calling Memora over REST, MCP, or the Go SDK instead of the CLI. |
| [Operations](operations.md) | Running Memora for more than one user: authentication, agent identity, and deployment. |
