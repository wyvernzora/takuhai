# AGENTS.md

Drop-in operating instructions for coding agents working on **takuhai**. Read the user-global rules first:

- `~/.agents/AGENTS.md` — universal agent-behavior rules (non-negotiables, simplicity, surgical changes, communication style, grilling, etc.)
- `~/.agents/go.md` — Go engineering rules (loaded because this repo has `go.mod`)

This file holds project-specific context, learnings, and overrides only. Rules in the global files apply unless explicitly contradicted here.

The canonical reference is [`docs/indexer-handover.md`](docs/indexer-handover.md) — the agreed design and build plan. Read it before any sizable change; the decisions log (§5) and invariants (§3) are settled — do not relitigate them.

---

## 1. Project context

### About takuhai

- **Name:** takuhai (宅配 — "home delivery / courier"); pairs with `kura` (蔵).
- **Domain:** anime **release indexer** — a dumb, durable store + work queue + query API. It records what an external matching agent reports; it holds no matching policy itself.
- **Consumer tools (MCP):** `get_releases` (by canonical `tvdb:NNN` ref).
- **Worker tools (MCP):** `claim_unmatched`, `submit_match`, `escalate`, `defer`, `reopen`, `get_queue_stats`.
- **Transports:** stdio (default), streamable HTTP (`--transport=http --addr=:8080`, MCP mounted at `/mcp`, health at `/healthz`).
- **Distribution:** Go binary, Docker container.

### Invariants (do not violate — see handover §3)

- Indexer is the store; the agent is the brain. No thresholds/heuristics in the indexer.
- Raw is immutable and kept forever. Match/attributes are a derived, recomputable layer.
- Canonical refs are **opaque** strings — never validated against TVDB/kura. The indexer never calls kura.
- Everything is idempotent: re-scrape, re-claim after crash, re-submit must all be safe.
- Sources are pluggable; adding a second source must not touch the ingestion core.

### Stack

- **Language:** Go 1.26.3+ (pinned in `go.mod` / `.tool-versions`).
- **Entry point:** `cmd/takuhai/main.go` — flag-driven, env fallbacks for all flags (prefix `TAKUHAI_`).
- **Store:** PostgreSQL via `pgx`. Migrations in `db/migrations/` (tool TBD — `goose`/`golang-migrate` — pick in Phase 1).
- **MCP SDK:** `github.com/modelcontextprotocol/go-sdk` (same as dmhy-mcp); streamable HTTP at `/mcp`, health at `/healthz`.
- **Logs:** structured `slog` (JSON) to stderr, `--log-level`.

### Package map (handover §12)

```
cmd/takuhai/         flag-driven entrypoint (serve)
internal/source/     Source interface + RawItem (+ dmhy/ adapter)
internal/ingest/     ingestion core (dedup, upsert, enqueue)
internal/store/      Postgres repo (releases, raw_items, match_events, queue)
internal/mcp/        MCP server: consumer + worker tool groups
db/migrations/       SQL migrations
```

### Commands

```sh
go run ./cmd/takuhai           # run from source (stdio)
go test ./...                  # full test suite
make check                     # fmt + vet + lint + test + build
make hooks                     # install the .githooks/ commit-message guard
```

### Sibling repos (bootstrap lineage)

`dmhy-mcp` is the direct sibling (Go MCP server, same author/domain) and the template for this repo's scaffolding; its `internal/dmhy` client is what the DMHY source adapter will wrap (handover §8 prefers lifting it into an importable package over copying). `kura` and `ariadne` are the other reference layouts.

---

## 2. Conventions

- **Commit messages must not mention Claude/AI tooling.** A `commit-msg` hook in `.githooks/` rejects any message containing "claude" (case-insensitive). Run `make hooks` once per checkout to enable it. Do **not** add `Co-authored-by: Claude` or "Generated with …" trailers.
- Match the surrounding style; keep changes surgical.

---

## 3. Project Learnings

**Accumulated corrections. This section is for the agent to maintain, not just the human.**

When the user corrects your approach, append a one-line rule here before ending the session. Write it concretely ("Always use X for Y"), never abstractly ("be careful with Y"). If an existing line already covers the correction, tighten it instead of adding a new one.

- (empty)
