# takuhai n8n nodes

Custom [n8n](https://n8n.io) nodes for **takuhai** — the anime release index. They let
n8n drive the whole loop: fetch from a crawler, push to `/ingest`, claim queue work,
submit matcher results, and get release details or magnet links.

## Design

### Nodes

| Node | Kind | Credential | What it does |
|---|---|---|---|
| **Takuhai** | action | Takuhai API | The takuhai service surface. Resource **Releases** (ingest / get release / get magnet link) or **Queue** (claim / submit / queue stats). |
| **Takuhai Crawler** | action + trigger | Takuhai Crawler API | Generic `POST /crawl` action plus a polling trigger that stores cursor/watermark state in n8n and emits one batch item only when new posts exist. |
| **Takuhai Queue Trigger** | trigger | Takuhai API | Polls `/queue/claim` and emits one batch item of claimed releases. |

Operation I/O (the cardinalities differ by design):

- **Releases → Ingest** — forwards the page's `posts` blob as one batched `/ingest`
  call → one summary item.
- **Releases → Get Release** — fetches one `infohash` via `/releases/{infohash}` and emits
  the release detail object unchanged.
- **Releases → Get Magnet Link** — fetches one `infohash` via `/magnets/{infohash}` and emits
  `{infohash, magnet}`.
- **Queue → Claim** — manual claim operation; emits one item containing `{items, count}`.
- **Queue → Submit Dispositions** — accepts one JSON body: a single disposition object, an array of
  disposition objects, one `{items}` batch object, or structured-output `{output:{items}}`;
  emits `{items:[{infohash, ok, error?}], count}`. HTTP 409 conflicts become per-item
  `{ok:false,error:"conflict"}` results; other errors still fail the node.
- **Queue → Get Queue Stats** — one stats item.
- **Crawler → Crawl** — one item = the page `{posts, next_cursor, has_more}`.
- **Crawler trigger** — keeps a node-local crawl watermark and emits one
  `{posts, count}` item when new posts exist. The reset option clears its saved cursor
  and watermark for one-off recovery/backfill.
- **Takuhai Queue Trigger** — claims on each poll and emits one item containing
  `{items, count}` so one AI agent call can handle the whole batch.

Claim and Ingest return endpoint envelopes in full so new server fields flow through
without a node change. Submit returns a compact per-disposition result because `/submit`
only returns `{"ok":true}`.

### The opaque-shuttle principle

n8n is **transport**, not a participant in the data. The `posts` payload (and the
`raw_items` evidence the matcher reads) is a sealed contract between the crawler and
takuhai — n8n forwards it verbatim and never models a `RawPost`. The only fields n8n
actually *speaks* are the control/fencing ones: `claim_token`, `infohash`, the crawler
`next_cursor`/`has_more`, and the queue counts. The matcher produces the submit body
as JSON. So these nodes pin to the **endpoint envelopes**, not the data schema: a new
`RawPost` field changes nothing here.

### Example: backfill loop

```
Loop ─► Takuhai Crawler (cursor = prev next_cursor)
     ─► Takuhai · Ingest (Posts = {{$json.posts}})
     ─► IF has_more is false: stop, else set cursor = next_cursor and loop
```

### Example: steady crawl

```
Takuhai Crawler trigger ─► Takuhai · Ingest (Posts = {{$json.posts}})
```

### Example: match loop

```
Takuhai Queue Trigger ─► [matcher over $json.items] ─► Takuhai · Submit Dispositions
```

## Packaging & deployment

Built as a **minimal init container** (`ghcr.io/wyvernzora/takuhai/n8n-nodes`), versioned
`vX.Y.Z` in lockstep with the service (`ghcr.io/wyvernzora/takuhai`) and crawler
images (`ghcr.io/wyvernzora/takuhai/crawler-dmhy`, `ghcr.io/wyvernzora/takuhai/crawler-nyaa`) — all published atomically by one CI release job and pinned together by a single deploy `version`.

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

All nodes and credentials share one icon —
[`docs/assets/logo-n8n.svg`](../../docs/assets/logo-n8n.svg). The build copies it into
each compiled node dir and credentials dir. The container image builds from the repo
root so that asset is in scope.

Local n8n: point `N8N_CUSTOM_EXTENSIONS` at this package, or symlink `dist` into
`~/.n8n/custom`.
