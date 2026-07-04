# takuhai — Operations

For architecture, see [design.md](design.md).

## Build And Run

```sh
make build
go build -o bin/takuhai ./cmd/takuhai

./bin/takuhai --addr=:8080
```

takuhai serves `/ingest`, `/magnets/{infohash}`, `/queue/claim`, `/queue/stats`,
`/submit`, `/mcp`, `/healthz`, and `/metrics`.

The DMHY crawler is separate:

```sh
(cd sources/dmhy && go build -o ../../bin/takuhai-dmhy ./cmd/takuhai-dmhy)
./bin/takuhai-dmhy serve --addr=:8081 --sort-id=2
```

The Nyaa crawler exposes the same `/crawl` shape:

```sh
(cd sources/nyaa && go build -o ../../bin/takuhai-nyaa ./cmd/takuhai-nyaa)
./bin/takuhai-nyaa serve --addr=:8082 --category=1_0 --filter=0
```

## Configuration

Every service flag honors a `TAKUHAI_` environment fallback.

| Flag | Env | Default | Notes |
| --- | --- | --- | --- |
| `--addr` | `TAKUHAI_ADDR` | `:8080` | HTTP listen address |
| `--database-url` | `TAKUHAI_DATABASE_URL` | unset | PostgreSQL URL; required |
| `--log-level` | `TAKUHAI_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `--queue-max-attempts` | `TAKUHAI_QUEUE_MAX_ATTEMPTS` | `3` | Failed unmatched submits before `exhausted` |

The DMHY crawler uses `TAKUHAI_DMHY_` variables for its own flags (`--addr`,
`--dmhy-base-url`, `--sort-id`, `--rate-rps`, `--cache-ttl`, `--log-level`).
The Nyaa crawler uses `TAKUHAI_NYAA_` variables (`--addr`, `--nyaa-base-url`,
`--query`, `--category`, `--filter`, `--rate-rps`, `--log-level`).

## Database

takuhai requires PostgreSQL. Embedded goose migrations run automatically before the
HTTP listener binds. A migration failure aborts startup; a database already at head is
a no-op.

Local development databases from older schemas should be recreated.

## Workflow

```text
n8n -> POST /crawl       -> crawler
n8n -> POST /ingest      -> takuhai
n8n -> POST /queue/claim -> wake and claim raw release evidence
n8n -> matcher agent     -> matched | unmatched | suppressed
n8n -> POST /submit      -> takuhai
n8n -> GET /magnets/{infohash} -> fetch a stored magnet link
consumer agent -> MCP list_releases / resolve_magnets
```

The n8n trigger claims work directly. `/queue/stats.exhausted` should be monitored as
an operator intervention signal.

## Security

takuhai has no application-level auth. Restrict write surfaces by infrastructure:
n8n should be the only caller of `/ingest`, `/magnets/*`, `/queue/*`, and
`/submit`; consumer agents should only reach `/mcp`. The service itself needs egress
only to Postgres and DNS.
Crawler deployments, not takuhai, own source-site egress.

This repo does not ship Kubernetes manifests. Platform policy belongs with the
deployment that runs takuhai.

## Releases

Push a semver tag such as `v0.1.0` to run `.github/workflows/release.yaml`. The
workflow verifies that the tagged commit is on `main` and has a successful `ci.yml`
push run, then publishes versioned and `latest` GHCR images for takuhai, the crawler
images, and the n8n node init image before creating the GitHub release.

## Health And Shutdown

- `/healthz` is a live DB ping.
- `/metrics` exports Prometheus metrics; see [`docs/metrics.md`](metrics.md).
- Startup fails fast if the HTTP bind fails.
- SIGTERM drains in-flight HTTP/MCP requests before closing the DB pool.
- Logs are JSON `slog` on stderr.

## Development

```sh
make hooks
make check
make devserver

for m in . sources/dmhy sources/nyaa; do (cd "$m" && go build ./... && go vet ./... && go test -race ./...); done

go test -tags=e2e -run TestEndToEndWorkflow -count=1 ./e2e
go test -race -tags=conformance ./...
go test -tags=smoke -run TestSmoke ./cmd/takuhai
```

`make devserver` runs `docker compose -f tools/devserver/compose.yaml up --build`: ephemeral
Postgres on `localhost:5432`, takuhai on `localhost:8080`, the DMHY crawler on
`localhost:8081`, and the Nyaa crawler on `localhost:8082`. Stop it with Ctrl-C; use
`docker compose -f tools/devserver/compose.yaml down` to remove the containers.

`make test` includes the Docker-backed e2e workflow test. Conformance, e2e, and smoke
use testcontainers and require Docker access.
