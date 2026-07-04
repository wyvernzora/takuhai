<div align="center">
    <br>
    <br>
    <img width="256" src="docs/assets/logo-full-256.png">
    <h1 align="center">宅配</h1>
</div>

<p align="center">
<b>takuhai - self-hosted anime release indexer</b>
</p>

<hr>
<br>
<br>

**takuhai** (宅配 — "home delivery / courier") is a self-hosted anime **release
index**: a dumb, durable store + work queue + query API. It receives raw releases
pushed from pluggable sources (DMHY first), keeps them immutably, dedups them by
infohash into a queryable catalog, and exposes the unmatched ones as a work queue.

It pairs with [`kura`](https://github.com/wyvernzora/kura) (蔵 — "storehouse").

## The idea

The hard direction of release management — *canonical series → search keywords* — is
intractable; the forward direction — *raw release name → canonical series* — is not.
takuhai inverts the problem: an **external matching agent** resolves each release to a
canonical ref once, at ingest. Consumers that already hold a ref then query
the index **directly by ref**, no keyword guessing.

takuhai holds **no matching intelligence** of its own. Canonical refs are opaque
strings it never resolves; it only records the matcher outcome.

## Architecture in one breath

n8n drives everything. A stateless **crawler** (`POST /crawl`) fetches posts; n8n
pushes them to takuhai's `POST /ingest`, which dedups and queues them. n8n drives the
**match loop** over the queue REST API; a stateless matcher resolves each
release. Consumers read the catalog over an **MCP** API (`list_releases`,
`resolve_magnets`). Postgres is both the store and the work queue. See
[docs/design.md](docs/design.md).

## Quick start

```sh
make devserver                                     # Postgres + takuhai + crawler services

make build                                          # → bin/takuhai
TAKUHAI_DATABASE_URL=postgres://… \
  ./bin/takuhai --addr=:8080                        # /ingest, /queue/*, /submit, /mcp, /healthz
```

The binary runs its migrations on startup. Config is flag- or `TAKUHAI_`-env driven
(`--addr`, `--database-url`, `--log-level`). See [docs/operations.md](docs/operations.md)
for deployment and the container build.

## Documentation

- [docs/design.md](docs/design.md) — architecture, data model, queue semantics,
  external contracts, invariants.
- [docs/operations.md](docs/operations.md) — build, configure, deploy, run, observe.
- [docs/conformance-matrix.md](docs/conformance-matrix.md) — the spec-clause → test map.

## Development

```sh
make hooks    # point git at .githooks/ (commit-message guard)
make check    # fmt + vet + lint + test + build
```

This is a Go workspace: the root service module + crawler modules under `sources/`.

## License

MIT — see [LICENSE](LICENSE).
