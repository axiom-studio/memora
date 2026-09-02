# CLI Reference

`memora-cli` is a command-line client for the Memora REST API, built from the same module as the server and shipping alongside it. Every command here talks to a running `memora-core` over HTTP, with one exception noted under content migration.

## Invocation

```text
memora-cli [global-flags] <command> [args...]
```

Global flags may appear before or after the command name. Most commands operate inside a workspace and fail with a usage error when none is set, either through `--workspace` or `MEMORA_WORKSPACE`.

## Global Flags

| Field | Value |
|---|---|
| `--endpoint` | Server URL. Environment `MEMORA_ENDPOINT`. Default `http://localhost:7777`. |
| `--api-key` | Bearer token. Environment `MEMORA_API_KEY`. |
| `--agent-id` | The writing agent. Environment `MEMORA_AGENT_ID`. Default `agent_opaque_local`. |
| `--workspace`, `-w` | Target workspace. Environment `MEMORA_WORKSPACE`. |
| `--output`, `-o` | Output format. Environment `MEMORA_OUTPUT`. Default `text`. |
| `--timeout` | Request timeout as a Go duration, such as `45s`. Default `30s`. |
| `--no-color` | Disable coloured output. |
| `--quiet`, `-q` | Suppress normal output. |
| `--verbose`, `-v` | Enable verbose output. |

Every write the CLI sends carries an agent ID, defaulting to `agent_opaque_local`. Attributing writes from different services to different agents is worth doing early, because the ledger and recall filters both key on it and the attribution cannot be reconstructed afterwards.

## Output Formats

| Field | Value |
|---|---|
| `text` | Human-readable. The default. |
| `json` | A single JSON document. |
| `jsonl` | JSON Lines, one record per line. |
| `yaml` | YAML. |

An unrecognised value falls back to `text` without an error, so a misspelled format is only visible in the output shape.

## Exit Codes

| Field | Value |
|---|---|
| `0` | Success. |
| `1` | Usage error — a missing argument, an unknown command, or no workspace set. |
| `2` | Authentication failure. The server returned `401`. |
| `3` | Server fault. The server returned a `5xx`. |
| `4` | Client error. Any other `4xx`, or a local failure such as an unreadable file. |
| `5` | Watermark conflict. The server returned `412`. |
| `8` | Not found. The server returned `404`. |

Exit code `5` is the one worth branching on in scripts: it means a conditional write lost a race and the correct response is to re-read and retry, not to abort.

```bash
if ! memora-cli patch "$MEM" --patch "$OPS" --if-match "$WMK"; then
    [ $? -eq 5 ] && echo "watermark moved; re-read and retry"
fi
```

## Workspace and Collection Commands

| Section | Description |
|---|---|
| `workspaces list` | List workspaces. |
| `workspaces create --name <name> [--region <region>]` | Create a workspace. |
| `workspaces show <ws_id>` | Read one workspace. |
| `workspaces delete <ws_id>` | Delete a workspace. |
| `collections list` | List collections in the current workspace. |
| `collections create <name>` | Create a collection. The name is positional, not a flag. |

## Memory Commands

| Section | Description |
|---|---|
| `imprint` | Create a Memory. Requires `--text` or `--from-file`. |
| `lookup <mem_id>` | Read a Memory with its Cells. |
| `update <mem_id>` | Replace a Memory's content. Requires a watermark. |
| `patch <mem_id>` | Apply find-and-replace operations. Requires a watermark. |
| `append <mem_id>` | Append to a Memory's content. |
| `forget <mem_id>` | Delete a Memory, cascading to its edges. |
| `list` | List Memories in the current workspace. |
| `watermarks <mem_id>` | Show watermark history for a Memory. |

### imprint

| Field | Value |
|---|---|
| `--text` | Content to store. Pass `-` to read standard input. |
| `--from-file` | Read content from a file instead. |
| `--collection` | Collection to file the Memory under. |
| `--chunker` | Override the workspace chunker for this imprint. |
| `--chunker-opt` | Chunker configuration as `key=value`. Repeatable. |
| `--tag` | Tag as `key=value`. Repeatable. |
| `--auto-link` | Force auto-linking on for this imprint. |
| `--no-auto-link` | Force auto-linking off for this imprint. |

### update, patch, and append

| Field | Value |
|---|---|
| `--text` | New content for `update` and `append`. Pass `-` for standard input. |
| `--from-file` | Read new content from a file. `update` only. |
| `--patch` | A JSON array of `{"old_string": …, "new_string": …}` operations. `patch` only. |
| `--patch-file` | Read the operations array from a file. `patch` only. |
| `--if-match` | The watermark the caller expects to be current. |

`append` takes `--text` only; unlike `update` it has no `--from-file`, so appending a file's contents means piping it in with `--text -`.

A `--if-match` value that no longer matches the Memory's head returns `412` and the CLI exits `5`, having changed nothing.

### list

| Field | Value |
|---|---|
| `--collection` | Restrict to one collection. |
| `--limit` | Maximum Memories to return. |

## Recall Commands

| Section | Description |
|---|---|
| `recall <query>` | Run a recall query. |
| `pin create --query <text>` | Save a recall query. |
| `pin list` | List saved queries. |
| `pin delete <pin_id>` | Delete a saved query. |

### recall

| Field | Value |
|---|---|
| `--mode` | `hybrid`, `keyword`, `vector`, or `lookup`. Default `hybrid`. |
| `--k` | Number of results. Default `5`. |
| `--collection` | Restrict to one collection. |
| `--neighbor-depth` | Expand this many hops along graph edges from each seed hit. |
| `--edge-types` | Comma-separated edge types to follow during expansion. |

### pin create

| Field | Value |
|---|---|
| `--query` | The query text. Required. |
| `--mode` | Recall mode to save. Default `hybrid`. |
| `--k` | Number of results to save. Default `10`. |
| `--watermark` | Bind the pin to a watermark, holding results at that point in time. |
| `--label` | An optional human-readable label. |

> **Pins default to `k = 10`, recall defaults to `k = 5`.** Saving a query and running it directly return different numbers of results unless `--k` is given explicitly on both.

## Graph Commands

| Section | Description |
|---|---|
| `link <src_mem_id> <tgt_mem_id> --type <edge_type>` | Create an edge between two Memories. |
| `unlink <edge_id>` | Remove an edge. |
| `neighbors <mem_id>` | One-hop neighbours of a Memory. |
| `traverse <seed_mem_id>` | Breadth-first traversal from a Memory, returned in layers. |
| `graph stats` | Node count and edge count by type for the workspace. |

| Field | Value |
|---|---|
| `--type` | Edge type for `link`. See [Concepts](concepts.md) for the seven types. |
| `--properties` | JSON properties to attach to the edge on `link`. |
| `--direction` | `out`, `in`, or `both`, for `neighbors` and `traverse`. |
| `--types` | Comma-separated edge types to filter by, for `neighbors` and `traverse`. |
| `--k` | Maximum neighbours to return, for `neighbors`. |
| `--depth` | Traversal depth, for `traverse`. Capped at `3`; a larger value returns `graph_traverse_depth_exceeded`. |

`unlink` refuses to remove a `vector_neighbor` edge, returning `cannot_unlink_synthetic_edge`, because those edges are written by the system rather than asserted by a caller.

## Agent Commands

| Section | Description |
|---|---|
| `agents list` | List agents registered in the workspace. |
| `agents register --agent-id <id>` | Register an agent. |

| Field | Value |
|---|---|
| `--agent-id` | The agent identifier to register. |
| `--provider` | The identity provider that vouches for it. |
| `--display-name` | A human-readable name. |

Explicit registration is optional for writing: an unknown agent that passes its identity check is registered automatically on first write. Registering ahead of time is how a display name and provider get attached.

## Operational Commands

| Section | Description |
|---|---|
| `health` | Liveness check against the server. |
| `ready` | Readiness check, reporting per-adapter status. |
| `version` | Print the client version, commit, and build date. |
| `migrate content` | Backfill Memory and Cell bodies into a content store. |

### migrate content

This is the one command that does not go through the REST API. It opens the metadata and content stores directly, so it must run on a host with access to the data directory rather than against a remote endpoint.

| Field | Value |
|---|---|
| `--workspace` | The workspace to migrate. Required. |
| `--collection` | Restrict the backfill to one collection. |
| `--data-dir` | Data directory holding the SQLite database. Environment `MEMORA_DATA_DIR`. Default `./data`. |
| `--metadata-driver` | Metadata driver to read from. Environment `MEMORA_METADATA_DRIVER`. Default `sqlite`. |
| `--content-driver` | Content driver to write to. Environment `MEMORA_CONTENT_DRIVER`. Default `sqlite`. |
| `--content-dsn` | Target DSN. Environment `MEMORA_CONTENT_DSN`. Defaults to the SQLite database for the `sqlite` driver, or `<data-dir>/content` otherwise. |
| `--dry-run` | Report counts without writing. |
| `--verify` | Compare content between the metadata store and the content store. |
| `--resume-from` | Continue from a given Memory ID after an interruption. |
| `--max-rate` | Memories per second. Default `100`. `0` removes the limit. |

The command prints a JSON result and exits `4` when `--verify` finds any mismatch, which makes a verification pass usable as a deployment gate. See [Operations](operations.md) for the migration procedure.
