# Memora Core

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev/)
[![Status: pre-alpha](https://img.shields.io/badge/Status-pre--alpha-orange.svg)]()

Memora is the **Memory-as-a-Service API for AI apps** — durable, multi-tenant,
versioned memory with vector + graph recall, callable from any MCP-aware agent.

> **Status:** pre-alpha. The OSS module layout and adapter contracts are being
> bootstrapped in public. See the [PRDs](docs/) for the full design.

## What's inside

- **memora-core** — single-binary HTTP server with bundled MCP transport.
- **memora-cli** — first-class command-line tool, built from the same Go module.
- **Pluggable adapters** behind three Go interfaces — `PrimaryStore`,
  `VectorStore`, `LedgerStore`. OSS ships SQLite + sqlite-vec + sqlite-ledger
  as zero-dependency defaults; Postgres + pgvector ship as scale-up adapters.
- **Context Graph** — typed, directed edges between Memories (`parent_of`,
  `derived_from`, `supersedes`, `references`, `session_of`, `mentions`) plus
  graph-augmented Recall.
- **Agent identity** — every Memory / Cell / Edge / Ledger entry carries an
  `agent_id`. Pluggable providers: `opaque`, `a2a`, `did`, `oauth_agent`,
  `oidc_agent`, `anthropic_session`.

## 60-second quickstart

```bash
git clone https://github.com/axiom-studio/memora && cd memora
make build

# 1) Start the server
./bin/memora-core serve --addr=:7777 --data-dir=./data &

# 2) Create a workspace + remember it
export MEMORA_ENDPOINT=http://localhost:7777
WS=$(./bin/memora-cli workspaces create --name demo -o json | jq -r .id)
export MEMORA_WORKSPACE=$WS

# 3) Imprint a memory
./bin/memora-cli imprint --text "Customer wants the enterprise plan."

# 4) Recall by keyword
./bin/memora-cli recall enterprise

# 5) Patch it — only the changed cell re-embeds (the moat)
WMK=$(./bin/memora-cli lookup mem_XXXX -o json | jq -r .head_watermark)
./bin/memora-cli patch mem_XXXX \
    --patch '[{"old_string":"enterprise","new_string":"premium"}]' \
    --if-match $WMK
#   ✓ Patched mem_XXXX
#     Cells re-embed: 1
#     Cells skipped:  0 (the moat)
```

Docker:

```bash
docker run -p 7777:7777 -v memora-data:/data ghcr.io/axiomstudio/memora-core:latest
```

Attach via MCP (Claude Code, etc.):

```bash
./bin/memora-core mcp --data-dir=./data
# JSON-RPC 2.0 over stdio; 25 OSS tools.
```

## Architecture

```
                ┌──────────────────────────────┐
   Clients ───▶ │    memora-core (HTTP + MCP)  │
                └──────────────────────────────┘
                          │     │     │
                   ┌──────┘     │     └──────┐
                   ▼            ▼            ▼
            PrimaryStore  VectorStore  LedgerStore
              (SQLite       (sqlite-vec   (SQLite
               / Postgres)   / pgvector)   / file
                                            / Postgres)
```

See [docs/architecture.md](docs/architecture.md) for the full architecture
walkthrough (forthcoming with the F12 docs milestone).

## Editions

- **Memora Core (this repo)** — Apache-2.0, self-hostable, zero-dependency
  default. Specified in the OSS PRD.
- **Memora Cloud** — managed multi-tenant SaaS by Axiom Studio, hosted MCP
  endpoint, AWS S3 Vectors VectorStore, SLAs, compliance attestations.
  Specified separately under the Cloud PRD.

The **public API surface is identical** across editions. Migration from OSS
to Cloud is an endpoint URL + API key change, no code changes.

## License

Apache-2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).

## Telemetry

Telemetry is **off by default** in OSS. Opt-in via `MEMORA_TELEMETRY=anonymous`
to send anonymous usage counts to Axiom Studio (helps roadmap prioritization).
