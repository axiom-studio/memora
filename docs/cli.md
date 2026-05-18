# memora-cli cookbook

`memora-cli` is the command-line client for Memora. It talks to a
local `memora-core` server (default `http://localhost:7777`) or any
remote Memora endpoint.

## Quick start

```bash
# Start the server.
memora-core serve --addr=:7777 --data-dir=./data

# In another terminal — point the CLI at it.
export MEMORA_ENDPOINT=http://localhost:7777

# Create a workspace.
memora-cli workspaces create --name my-app
# → ws_01HXYZ...

# Set it as the active workspace.
export MEMORA_WORKSPACE=ws_01HXYZ...

# Imprint a memory.
memora-cli imprint --text "Customer wants the enterprise plan."
# → mem_01HXYZ... (watermark wmk_01HXYZ...)

# Recall it.
memora-cli recall enterprise
# → [1] score=0.4 via=seed mem=mem_01HXYZ... "Customer wants the enterprise plan."

# Patch — only the changed cell re-embeds.
memora-cli patch mem_01HXYZ... \
    --patch '[{"old_string":"enterprise","new_string":"premium"}]' \
    --if-match wmk_01HXYZ...
# → Cells re-embed: 1
#   Cells skipped:  0 (the moat)
```

## Global flags

| Flag | Env | Default |
|---|---|---|
| `--endpoint URL` | `MEMORA_ENDPOINT` | `http://localhost:7777` |
| `--api-key KEY` | `MEMORA_API_KEY` | (empty — local-dev mode) |
| `--agent-id ID` | `MEMORA_AGENT_ID` | `agent_opaque_local` |
| `--workspace ID`, `-w ID` | `MEMORA_WORKSPACE` | (required for memory verbs) |
| `--output FMT`, `-o FMT` | `MEMORA_OUTPUT` | `text` (`json`, `jsonl`) |
| `--timeout DURATION` | — | `30s` |

## Exit codes

- `0` success
- `1` usage error
- `2` auth error (401)
- `3` server error (5xx)
- `4` other client error (4xx)
- `5` CAS conflict (412) — for scripted retry
- `8` not found (404)

## Common workflows

### Bulk-load a directory of notes

```bash
for f in notes/*.md; do
  memora-cli imprint --from-file "$f" --tag source=$(basename "$f")
done
```

### Search with Context Graph expansion

```bash
memora-cli recall "pricing decision" \
  --mode hybrid --k 10 \
  --neighbor-depth 1 \
  --edge-types references,derived_from
```

### Link two memories

```bash
memora-cli link mem_abc mem_def --type references
memora-cli neighbors mem_abc --direction both
```

### Patch a long memory and verify the moat

```bash
WMK=$(memora-cli lookup mem_abc -o json | jq -r .head_watermark)
memora-cli patch mem_abc \
  --patch '[{"old_string":"old phrase","new_string":"new phrase"}]' \
  --if-match "$WMK" \
  -o json | jq .cells_re_embedded
# → 1   (the rest stayed embedded)
```

### Health checks

```bash
memora-cli health
memora-cli ready    # also pings every configured adapter
```

## Output formats

`-o json` and `-o jsonl` are stable; pipe them through `jq`:

```bash
memora-cli list -o json | jq -r '.memories[] | .id + "\t" + (.content | .[:60])'
```

## Using a remote endpoint

```bash
memora-cli --endpoint https://memora.example.com \
  --api-key mka_live_xxx \
  -w ws_prod \
  imprint --text "..."
```

## Reading from stdin

```bash
git log --oneline -50 | memora-cli imprint --text -
```

## MCP attach

For agent loops, attach memora-core's MCP server directly over stdio:

```bash
memora-core mcp --data-dir=./data
# Reads JSON-RPC 2.0 frames on stdin, writes responses on stdout.
# Stderr remains free for logs.
```

Wire it from Claude Code via `.mcp.json`:

```json
{
  "mcpServers": {
    "memora": {
      "command": "memora-core",
      "args": ["mcp", "--data-dir", "/path/to/data"]
    }
  }
}
```

The MCP server exposes all 25 OSS tools (memora_imprint, _recall,
_patch, _link, _neighbors, _traverse, etc.).
