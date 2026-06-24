# takuhai n8n nodes

Custom [n8n](https://n8n.io) nodes for **takuhai** — the anime release index. They let
n8n drive the whole loop: fetch from a crawler, push to `/ingest`, claim queue work,
and submit matcher results.

## Design

### Nodes

| Node | Kind | Credential | What it does |
|---|---|---|---|
| **Takuhai** | action | Takuhai API | The takuhai service surface. Resource **Ingest** (push posts) or **Queue** (claim / submit / queue stats). |
| **Takuhai Crawler** | action | Takuhai Crawler API | Generic `POST /crawl` against *any* takuhai-shaped crawler (DMHY first). One node, one credential per crawler. |
| **Takuhai Trigger** | trigger | Takuhai API | Polls `/queue/claim` and emits claimed releases. |

Operation I/O (the cardinalities differ by design):

- **Ingest → Ingest Posts** — forwards the page's `posts` blob as one batched `/ingest`
  call → one summary item.
- **Queue → Claim** — manual claim operation; **fans out** one output item per claimed
  release.
- **Queue → Submit** — per input item; **passes the item through** and annotates it with
  the result.
- **Queue → Get Queue Stats** — one stats item.
- **Crawler → Crawl** — one item = the page `{posts, next_cursor, has_more}`.
- **Takuhai Trigger** — claims on each poll and emits one item per claimed release.

Responses are returned **in full** — each node passes the endpoint's envelope through
verbatim (Claim emits the whole `ClaimItemResult`; Ingest the whole summary), so a new
server field flows through without a node change.

### The opaque-shuttle principle

n8n is **transport**, not a participant in the data. The `posts` payload (and the
`raw_items` evidence the matcher reads) is a sealed contract between the crawler and
takuhai — n8n forwards it verbatim and never models a `RawPost`. The only fields n8n
actually *speaks* are the control/fencing ones: `claim_token`, `infohash`, the crawler
`next_cursor`/`has_more`, and the queue counts — plus the matcher's verdict it
maps into `status`. So these nodes pin to the **endpoint envelopes**, not the data
schema: a new `RawPost` field changes nothing here.

### Example: backfill loop

```
Loop ─► Takuhai Crawler (cursor = prev next_cursor)
     ─► Takuhai · Ingest (Posts = {{$json.posts}})
     ─► IF has_more is false: stop, else set cursor = next_cursor and loop
```

### Example: match loop

```
Takuhai Trigger ─► [matcher] ─► Takuhai · Submit
```

## Packaging & deployment

Built as a **minimal init container** (`ghcr.io/wyvernzora/takuhai/n8n-nodes`), versioned
`vX.Y.Z` in lockstep with the service (`ghcr.io/wyvernzora/takuhai`) and the crawler
(`ghcr.io/wyvernzora/takuhai/crawler-dmhy`) — all three published atomically by one CI
release job and pinned together by a single deploy `version`.

The init container ships the built nodes and, on pod start, copies them into an
`emptyDir` that n8n mounts and reads via `N8N_CUSTOM_EXTENSIONS`:

```
initContainer (takuhai/n8n-nodes)  ──cp──►  emptyDir /opt/n8n/custom  ◄──  n8n container
                                            N8N_CUSTOM_EXTENSIONS=/opt/n8n/custom
```

Updates ride the rollout: bump `version` → n8n restarts → init repopulates the volume →
n8n loads the new nodes at boot. The deployment system owns the manifest shape.

## Development

Uses **corepack pnpm** (pinned via `packageManager` in `package.json`).

```sh
corepack enable
corepack pnpm install --frozen-lockfile
corepack pnpm build            # tsc → dist/ + node icon
```

All three nodes share one icon — the repo brand asset
[`docs/assets/logo-face.svg`](../../docs/assets/logo-face.svg). The build copies it into
each compiled node dir, so change the logo there (not per node). The container image
builds from the repo root so that asset is in scope.

Local n8n: point `N8N_CUSTOM_EXTENSIONS` at this package, or symlink `dist` into
`~/.n8n/custom`.
