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
| A stale `claim_token` cannot submit after a newer claim | `TestAPIShape_StaleClaimTokenRejected` |
| Repeated `unmatched` submissions exhaust after the configured max attempts | `TestAPIShape_UnmatchedExhaustsAfterMaxAttempts` |

The real-binary smoke test covers startup migrations, `/healthz`, `/ingest`,
`/queue/claim`, `/submit`, `/queue/stats`, MCP tool registration/call, removed worker
path rejection, and fail-fast bind behavior.
