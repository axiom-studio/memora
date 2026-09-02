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

Workspace IDs are prefixed ULIDs, so `$MEMORA_WORKSPACE` now holds something shaped like `ws_01HXYZ...`.

## Storing the First Memory

```bash
./bin/memora-cli imprint --text "Customer wants the enterprise plan."
```

The response reports the new Memory's ID, its opening watermark, an MD5 of the content, how many Cells were created, and whether the Memory is ready to be recalled:

```json
{
  "memory_id": "mem_01HXYZ...",
  "watermark": "wmk_01HXYZ...",
  "content_md5": "…",
  "cells_created": 1,
  "recall_ready": true,
  "written_by_agent_id": "agent_opaque_local",
  "ledger_id": "lg_01HXYZ...",
  "latency_ms": 4
}
```

Embedding runs inline as part of the write, which is why `recall_ready` is already true rather than pending. It also means a large imprint holds the request open for as long as the embedding provider takes.

The `written_by_agent_id` is `agent_opaque_local` because that is the CLI's default agent. Every write is attributed to some agent; override it with `--agent-id` or the `MEMORA_AGENT_ID` environment variable.

## Recalling It

```bash
./bin/memora-cli recall enterprise
```

Recall defaults to `hybrid` mode and returns the top 5 hits. Hybrid runs both a keyword scan and a vector search and merges them by weighted score, weighting vector results `0.7` against keyword results `0.3`.

> **The default embedding provider is a stub.** Out of the box the embedding model is `noop:default`, so vector and hybrid recall return results with no semantic meaning — a keyword match is doing all the real work above. Set a real provider before drawing any conclusion about recall quality. See [Adapters](adapters.md) for the available providers.

## Patching It

Patch is where Memora differs most from a plain document store. It applies find-and-replace operations under a watermark check, re-chunks the result, and only re-embeds the Cells whose text actually changed.

Read the current watermark, then supply it as the expected value:

```bash
WMK=$(./bin/memora-cli lookup mem_01HXYZ... -o json | jq -r .head_watermark)

./bin/memora-cli patch mem_01HXYZ... \
    --patch '[{"old_string":"enterprise","new_string":"premium"}]' \
    --if-match "$WMK"
```

The response reports how much embedding work the edit avoided:

```json
{
  "memory_id": "mem_01HXYZ...",
  "watermark": "wmk_01HXYZ...",
  "patches_applied": 1,
  "cells_re_embedded": 1,
  "cells_skipped": 0,
  "last_modified_by_agent_id": "agent_opaque_local",
  "ledger_id": "lg_01HXYZ...",
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

This speaks JSON-RPC 2.0 over stdio and exposes tools covering workspaces, collections, memories, recall, and agents. It reads the same data directory as `serve`, so a stdio MCP session and an HTTP client can share one instance.

> **The stdio transport is unauthenticated unless you give it a key.** With no key configured it trusts its caller completely — it only talks to the process that started it — and logs a warning saying so. Passing `--api-key`, or setting `MEMORA_API_KEY`, makes the `initialize` handshake require that key and gates every other method behind it.

## Where to Go Next

| Section | Description |
|---|---|
| [Concepts](concepts.md) | What a Cell, a watermark, and an edge actually are, and how recall scores results. |
| [Configuration](configuration.md) | Moving off the defaults: storage drivers, a real embedding model, TLS. |
| [CLI Reference](cli.md) | The full command set, including the graph and audit commands not shown here. |
| [API Reference](api.md) | Calling Memora over REST, MCP, or the Go SDK instead of the CLI. |
| [Operations](operations.md) | Running Memora for more than one user: authentication, agent identity, and deployment. |
