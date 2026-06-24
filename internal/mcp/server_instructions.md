Takuhai is a passive anime release index and work queue. It stores crawled release posts and records matcher dispositions, but it does not decide matching policy itself.

This MCP surface is read-only and consumer-facing. Queue claiming, ingest, and matcher submission are REST-only.

Use `list_releases` to page through matched releases for one opaque metadata ref such as `tvdb:12345`. Treat refs as opaque `namespace:value` strings; do not infer behavior from the namespace beyond preserving it exactly.

Use `resolve_magnets` only after choosing releases by infohash. It returns full stored magnet URIs for known infohashes and omits unknown infohashes.

Infohashes are release identity in Takuhai. Copy infohashes, refs, cursors, and magnet URIs exactly as returned.
