# takuhai — Design

takuhai is a durable anime release index: it stores crawled release posts, leases
unmatched releases to an external matcher, records the matcher outcome, and lets a
consumer agent list matched releases by canonical ref.

## Invariants

- The release identity is the canonical v1 `btih`: 40 lowercase hex. Pure v2 torrents
  are skipped.
- takuhai does not match titles. It records the external matcher result.
- `ref` values are opaque namespace-prefixed strings such as `tvdb:123`; takuhai only
  shape-validates them.
- Crawlers are stateless. n8n owns schedules, cursors, retries, and orchestration.
- Queue claims are fenced by `claim_token`; stale submits must not overwrite newer
  claims.
- takuhai stores the full crawler-provided magnet link. It does not normalize,
  refresh, probe, or reassemble tracker URLs.

## Data Model

The schema has three tables:

- `releases`: one row per infohash. Holds representative title, full magnet,
  `size_bytes`, first-seen `published_at`, source set, match status, `ref`,
  confidence, claim bookkeeping, and timestamps.
- `raw_items`: append-only parsed crawler posts keyed by `(source, source_id)`.
- `match_events`: minimal append-only submit log with `status`, `ref`, `confidence`,
  `reason`, and `created_at`.

Release statuses are deliberately small:

- `unmatched`: not matched yet and claimable when not leased and under the failed-attempt cap.
- `matched`: matched to a canonical ref the user cares about.
- `suppressed`: not wanted, matched or not.
- `exhausted`: too many failed attempts; no longer offered as work.

No `defer`, `escalate`, `reopen`, `next_eligible_at`, recheck state, provenance fields,
or matcher attributes exist in this pass.

## REST API

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/ingest` | Accept a batch of crawler posts. |
| `POST` | `/queue/claim` | Lease claimable unmatched releases. |
| `GET` | `/queue/stats` | Return queue/status counts, including exhausted. |
| `POST` | `/submit` | Submit `matched`, `unmatched`, or `suppressed` for a claim. |
| `GET` | `/healthz` | DB ping. |
| `GET` | `/metrics` | Prometheus metrics. |

Crawler posts and ingest posts use the same shape:

```json
{
  "source": "dmhy",
  "source_id": "721238",
  "title": "raw release title",
  "magnet": "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
  "url": "https://share.dmhy.org/topics/view/721238_example.html",
  "published_at": "2026-06-24T12:00:00Z",
  "size_bytes": 3600000000
}
```

`/queue/claim` returns `claim_token`, `attempt_count`, `lease_expires_at`, and linked
`raw_items`. `/submit` accepts:

```json
{
  "infohash": "0123456789abcdef0123456789abcdef01234567",
  "claim_token": 12,
  "status": "matched",
  "ref": "tvdb:123",
  "confidence": 0.94,
  "reason": "title and episode numbering match"
}
```

`ref` and `confidence` are required only for `matched`. `reason` is plain debugging
text.

## Queue Semantics

Claiming an item stamps `claimed_at`, sets `lease_expires_at`, and bumps
`claim_token`. A matching submit must echo the current token.

Submitting `matched` or `suppressed` clears the lease and makes the status terminal.
Submitting `unmatched` increments `attempt_count` and keeps the lease in place; the
timeout is the retry mechanic. When the configured failed-attempt cap is reached, an
unmatched result becomes `exhausted`. Expired unmatched rows at or above the cap are
marked exhausted before new claims are offered. Claim crashes do not increment
`attempt_count`.

## MCP API

The MCP surface is read-only:

- `list_releases({ref, since?, limit?, cursor?})` returns matched releases with
  `infohash`, `title`, `size_bytes`, `published_at`, `confidence`, `sources`, and
  `next_cursor`.
- `resolve_magnets({infohashes})` returns `{ "magnets": { "<infohash>": "<magnet>" } }`.
  Unknown infohashes and known releases without magnets are omitted.
  Returned magnets are the stored full magnet strings.
