# AGENTS.md

Drop-in operating instructions for coding agents working on **takuhai**. Read the
user-global rules first:

- `~/.agents/AGENTS.md` — universal agent-behavior rules (non-negotiables, simplicity,
  surgical changes, communication style, etc.)
- `~/.agents/go.md` — Go engineering rules (loaded because this repo has `go.mod`)

This file holds project-specific context, learnings, and overrides only. Global rules
apply unless explicitly contradicted here.

**The canonical reference is [`docs/design.md`](docs/design.md)** — the load-bearing
design (identity model, data model, queue semantics, external contracts, seams). Its
§2 invariants are settled; do not relitigate them. For deployment and ops see
[`docs/operations.md`](docs/operations.md); for the spec→test map see
[`docs/conformance-matrix.md`](docs/conformance-matrix.md). Read design.md before any
sizable change.

---

## 1. Project context

### About takuhai

- **Name:** takuhai (宅配 — "home delivery / courier"); pairs with `kura` (蔵).
- **Domain:** anime **release index** — a dumb, durable store + work queue + query
  API. It records what an external matching agent reports; it holds no matching policy.
- **Ingestion is external push.** n8n drives a stateless crawler (`POST /crawl`) and
  pushes posts to takuhai (`POST /ingest`). takuhai holds no cursor, runs no scheduler,
  makes no outbound calls.
- **Surfaces:**
  - REST (n8n-driven): `POST /ingest`, `POST /queue/claim`, `GET /queue/stats`,
    `POST /submit`, `GET /magnets/{infohash}`, and `GET /releases/{infohash}`.
  - MCP (consumer-only): `list_releases`, `get_release`, `resolve_magnets`, over streamable HTTP at `/mcp`.
  - `/healthz` — a live DB ping.
- **Transport:** HTTP (`--addr=:8080`).
- **Distribution:** Go binary + Docker container. The crawler is a separate binary
  (`sources/dmhy/cmd/takuhai-dmhy`).

### Invariants (do not violate — see design.md §2)

- The indexer is the store; the matcher is the brain. No thresholds/heuristics in the
  indexer.
- A release *is* its infohash (canonical = 40-hex v1 btih; base32 decoded first; pure
  v2 skipped).
- Raw is immutable and kept forever; the match is a derived, recomputable layer.
- Canonical refs are **opaque** strings — shape-validated only, never checked against
  TVDB/kura. The indexer never calls kura.
- Everything is idempotent: re-ingest, re-claim after a crash, re-submit must all be
  safe (a disposition fences on `claim_token`).
- Sources are pluggable: a new source is a new crawler speaking the same `RawPost`
  shape — it must not touch takuhai's core.

### Stack

- **Language:** Go 1.26.3+ (pinned in `go.mod` / `.tool-versions`). It is a Go
  **workspace** (`go.work`): root service module + `sources/dmhy` crawler module.
- **Entry point:** `cmd/takuhai/main.go` — flag-driven, env fallbacks (prefix
  `TAKUHAI_`). Runs migrations at startup, then serves HTTP.
- **Store:** PostgreSQL via `pgx`. Migrations are **embedded goose** in
  `db/migrations/`, run at startup under an advisory lock.
- **MCP SDK:** `github.com/modelcontextprotocol/go-sdk` (streamable HTTP at `/mcp`).
- **Logs:** structured `slog` (JSON) to stderr, `--log-level`.

### Package map

```
cmd/takuhai/         flag-driven entrypoint: wiring and lifecycle (migrate → serve → drain)
pkg/rawpost/         shared wire contract: RawPost + IngestSummary (a leaf)
internal/config/     Config struct; flag + env binding; validation
internal/infohash/   NormalizeInfohash + ErrSkipInfohash — the dedup key
internal/cursor/     list_releases cursor encode/decode + ref/path binding + ref validation
internal/dispatch/   transport-neutral worker/consumer dispatch + sentinel→code helper
internal/rest/       REST /ingest, /queue/*, and /submit handlers
internal/store/      Store interface + param/result types + sentinel errors
internal/store/postgres/  pgx implementation (only backend in v1)
internal/mcp/        MCP server: consumer tools only; HTTP /mcp; calls dispatch
internal/health/     /healthz: DB ping via the Ping seam
db/migrations/       embedded goose SQL migrations
sources/dmhy/        stateless DMHY crawler module (own go.mod): RSS+HTML parsers + POST /crawl
```

`store` is a leaf (imports neither `rawpost` nor the REST layer); `mcp` reaches store
only through `dispatch`; REST queue/submit routes use `dispatch`, while ingest maps
`rawpost.RawPost` to store params at the boundary. The root module never imports
`sources/dmhy`.

### Commands

```sh
make check                     # fmt + vet + lint + test + build
make hooks                     # install the .githooks/ commit-message guard
go test -race -tags=conformance ./...        # conformance suite (real PG via testcontainers)
go test -tags=smoke -run TestSmoke ./cmd/takuhai   # real-binary smoke
# per-module (matches CI; root ./... does not traverse go.work):
for m in . sources/dmhy; do (cd "$m" && go build ./... && go vet ./... && go test -race ./...); done
```

---

## 2. Conventions

- **Commit messages must not mention Claude/AI tooling.** A `commit-msg` hook in
  `.githooks/` rejects any message containing "claude" (case-insensitive). Run
  `make hooks` once per checkout. Do not add `Co-authored-by` / "Generated with …"
  trailers.
- Match the surrounding style; keep changes surgical.

---

## 3. Project Learnings

**Accumulated corrections. This section is for the agent to maintain.** When the user
corrects your approach, append a one-line, concrete rule here before ending the session.

- The conformance suite drives in-process seams (`httptest`, direct dispatch) and **never
  boots the binary**, so deploy-shape bugs — MCP transport wiring, startup migrations,
  fail-fast bind, the graceful-drain order — are invisible to it. Validate the binary
  end-to-end with the real-binary smoke (`go test -tags=smoke -run TestSmoke
  ./cmd/takuhai`), not the conformance gate alone.
- `attempt_count` means failed unmatched submissions, not claims; claim crashes must
  not affect matching semantics.
