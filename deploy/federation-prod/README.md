# federation-prod — 3-node Postgres + pgvector topology

A reference topology: three memora-core nodes, each with its own database on a
shared Postgres 17 + pgvector instance. It is **not production-ready as
shipped**. Treat it as a starting point.

## Before `docker compose up`

```bash
cd deploy/federation-prod
cp .env.example .env
# fill every value, e.g. with: openssl rand -hex 24
docker compose up -d
```

Compose refuses to start until `POSTGRES_PASSWORD` and
`MEMORA_NODE{1,2,3}_API_KEY` are set. Nothing secret is committed: each node's
API key and Postgres DSNs are injected as `MEMORA_API_KEY` and
`MEMORA_{METADATA,VECTOR,LEDGER,CONTENT}_DSN` (see `docker-compose.yml`).
Because the password is embedded in connection URLs, keep it URL-safe (hex or
`[A-Za-z0-9]`).

Postgres is reachable only on the compose network (`postgres:5432`) and is not
published on the host. The nodes publish 7771–7773 and require their API key.

## Not covered here, so add it before real production use

- **TLS**: node-to-Postgres DSNs use `sslmode=disable`, and the nodes serve
  plain HTTP. Put them behind TLS (`server.tls.*`, or a terminating proxy) and
  use `sslmode=verify-full`.
- **Least-privilege database role**: `POSTGRES_USER=memora` is the
  superuser. Create a non-superuser owner for the three databases.
- **Backups and secret rotation** for the Postgres volume and the API keys.
- **Context graph**: disabled (`graph_driver = "none"`), because no Postgres
  GraphStore exists yet.
- **Federation fan-out** is configured but not wired into recall yet
  (Issue #5022).
