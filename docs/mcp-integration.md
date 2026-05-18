# MCP integration

Memora Core ships an in-process MCP server that speaks JSON-RPC 2.0
over stdio. The transport is the same one Claude Code uses; any
MCP-compatible client can attach.

## Anthropic Claude Code

In a project's `.mcp.json`:

```json
{
  "mcpServers": {
    "memora": {
      "command": "memora-core",
      "args": ["mcp", "--data-dir", "/path/to/data"],
      "env": {
        "MEMORA_EMBEDDING_MODEL": "openai:text-embedding-3-small",
        "OPENAI_API_KEY": "${OPENAI_API_KEY}"
      }
    }
  }
}
```

Claude Code spawns `memora-core mcp`, pipes JSON-RPC over its
stdin/stdout, and presents the 25 OSS tools to the model.

## Available tools

| Tool | Purpose |
|---|---|
| `memora_imprint` | Create a Memory. |
| `memora_lookup` | Fetch a Memory. |
| `memora_update` / `memora_patch` / `memora_append` | Edit a Memory (CAS). |
| `memora_forget` | Soft-delete + cascade edges. |
| `memora_recall` | Search (lookup/keyword/vector/hybrid + graph_expansion). |
| `memora_pin` / `memora_unpin` | Bind a recall to a watermark. |
| `memora_list_workspaces` + `_create_workspace` / `_get_workspace` / `_delete_workspace` | Workspace admin. |
| `memora_list_collections` / `_create_collection` / `_delete_collection` | Collections. |
| `memora_list_memories` | Page through a workspace. |
| `memora_get_watermark_history` | Version history. |
| `memora_register_agent` / `memora_list_agents` | Agent registry. |
| `memora_link` / `memora_unlink` / `memora_neighbors` / `memora_traverse` | Context Graph. |

## OpenAI tool use

Wrap each MCP tool as an OpenAI function schema — the
`inputSchema` returned by `tools/list` is already JSON Schema
draft-07. A reference adapter ships in the Python and TypeScript
SDKs (v0.3).

## Manual smoke test

```bash
# In one terminal:
memora-core mcp --data-dir=./data 2>/dev/null

# In another terminal:
(
  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memora_create_workspace","arguments":{"name":"demo"}}}'
) | memora-core mcp --data-dir=./data
```

You'll see initialize → tools list (25 tools) → workspace created.

## Auth

The stdio transport runs in-process — no over-the-wire auth needed.
The WebSocket transport (v0.5) will require a Bearer token matching
`MEMORA_API_KEY`.

## Agent identity

Every write tool (memora_imprint / update / patch / append / forget /
link / unlink) requires an `agent_id` argument. The default
`opaque` provider accepts anything. To enforce verification, point
the deployer's `MEMORA_IDENTITY_PROVIDER=anthropic_session` and have
the client supply `identity_proof = {session_id, model, system_prompt}`.

See [`pkg/identity`](../pkg/identity) for the derivation recipe used
by `anthropic_session`.
