Fetch one release by infohash.

Input is `{ "infohash": "..." }`.

Response is the release detail object: representative release fields, current match state, raw source evidence, and chronological match history. `raw_items` are ordered by `id` ascending. `match_events` are ordered by `created_at` ascending, then `id` ascending.

The detail response includes the full stored magnet because this is the single-release full context view. List tools stay magnet-free; use `resolve_magnets` when starting from a list result.

`match_events` is intentionally unpaginated for v1. Revisit pagination only if event counts grow enough to make responses large.

Errors use MCP tool-error content with code `invalid_input` for malformed infohashes and `no_such_release` for unknown releases.
