# takuhai

**takuhai** (宅配 — "home delivery / courier") is a self-hosted anime **release
indexer**: a dumb, durable store + work queue + query API. It continuously
ingests releases from pluggable sources (DMHY first), keeps the raw records
immutably, dedups them by infohash into a queryable catalog, and exposes the
unmatched ones as a work queue over MCP.

It pairs with [`kura`](https://github.com/wyvernzora/kura) (蔵 — "storehouse").
The hard direction — *canonical identity → good search keywords* — is solved
once at ingest: an **external** matching agent claims unmatched releases,
resolves each to a canonical `tvdb:NNN` ref plus attributes, and reports the
result back. Consumers that already hold a ref then query the index directly,
no keyword guessing.

The indexer holds **no matching intelligence**. Canonical refs are opaque
strings it never validates; its only defense against a bad match is provenance
(confidence + evidence + agent id) and revisability. See
[`docs/indexer-handover.md`](docs/indexer-handover.md) for the full design.

> **Status:** bootstrap skeleton. The server is not yet wired — see the build
> plan in the handover (§13). Phase 0 (backfill spike) is the starting point.

## Build & run

```sh
go build -o bin/takuhai ./cmd/takuhai
./bin/takuhai --transport=stdio
./bin/takuhai --transport=http --addr=:8080
```

HTTP transport will expose the MCP endpoint at `/mcp` and a liveness probe at
`/healthz`.

## Container

```sh
docker build -t takuhai .
docker run --rm -p 8080:8080 takuhai            # HTTP on :8080
docker run --rm -i takuhai --transport=stdio    # stdio
```

## Configuration

All flags honor a `TAKUHAI_`-prefixed environment-variable fallback.

| Flag | Env | Default |
| --- | --- | --- |
| `--transport` | `TAKUHAI_TRANSPORT` | `stdio` |
| `--addr` | `TAKUHAI_ADDR` | `:8080` |
| `--database-url` | `TAKUHAI_DATABASE_URL` | _(unset)_ |
| `--log-level` | `TAKUHAI_LOG_LEVEL` | `info` |

## Development

```sh
make hooks    # point git at .githooks/ (commit-message guard)
make check    # fmt + vet + lint + test + build
```

## License

MIT — see [LICENSE](LICENSE).
