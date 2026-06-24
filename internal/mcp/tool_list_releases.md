Return matched releases for one canonical ref, newest first, with cursor pagination.

Input is a JSON object with:

- `ref` (string, required): opaque metadata ref in `namespace:value` form.
- `since` (RFC3339 timestamp, optional): when present, page the delta path by first matched time instead of the catalog path by published time.
- `limit` (integer, optional): maximum releases to return. Server defaults and caps apply.
- `cursor` (string, optional): opaque `next_cursor` from a previous response. Pass it back exactly and only with the same `ref` and same `since` presence.

Response is `{ "releases": [...], "next_cursor": "..." }`. Each release includes `infohash`, `title`, `size_bytes`, `published_at`, `confidence`, and `sources`.

Errors use MCP tool-error content with code `invalid_ref` or `invalid_cursor` when the ref or cursor shape is invalid.
