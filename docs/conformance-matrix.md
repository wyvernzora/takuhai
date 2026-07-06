# Conformance Matrix

The tagged conformance suite is intentionally small after the API-shape reset. It pins
the public contract that should not drift.

Run it with:

```sh
go test -tags=conformance ./internal/conformance
```

## Current Coverage

| Contract | Test |
| --- | --- |
| Claim returns `claim_token`; submit `matched`; MCP `list_releases` includes confidence; `resolve_magnets` wraps magnets and omits unknowns | `TestAPIShape_MatchListsReleaseAndResolvesMagnet` |
| Single release detail is available over REST and dispatch, includes raw evidence and chronological match history, keeps magnet in detail, and orders `raw_items` by `id ASC` plus `match_events` by `created_at ASC, id ASC` | `TestAPIShape_GetReleaseDetail` |
| Release detail preserves absent facts as explicit JSON `null` (`ref`, `confidence`, `first_matched_at`, `magnet`, `size_bytes`, `url`), always renders `raw_items`/`match_events`/`sources` as arrays, and excludes lease internals (`claim_token`, `claimed_at`, `lease_expires_at`) | `TestAPIShape_GetReleaseExplicitNullsAndNoLeaseInternals` |
| `GET /releases/{infohash}` maps malformed infohashes to `400 invalid_input` and unknown releases to `404 no_such_release` | `TestAPIShape_GetReleaseRESTErrors` |
| MCP `get_release` maps unknown releases to tool error code `no_such_release` | `TestAPIShape_GetReleaseMCPNoSuchRelease` |
| A stale `claim_token` cannot submit after a newer claim | `TestAPIShape_StaleClaimTokenRejected` |
| Repeated `unmatched` submissions exhaust after the configured max attempts | `TestAPIShape_UnmatchedExhaustsAfterMaxAttempts` |

The real-binary smoke test covers startup migrations, `/healthz`, `/ingest`,
`/magnets/{infohash}`, `/releases/{infohash}`, `/queue/claim`, `/submit`, `/queue/stats`, MCP tool
registration/call, removed worker path rejection, and fail-fast bind behavior.
