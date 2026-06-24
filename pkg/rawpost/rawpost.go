// Package rawpost holds the shared push-ingestion wire contract: the RawPost
// shape exchanged between a crawler's POST /crawl response and takuhai's
// POST /ingest request, plus the IngestSummary that POST /ingest returns. It is
// a leaf — it imports nothing internal — so both the takuhai service module and
// the nested sources/dmhy crawler module can depend on it without coupling.
package rawpost

import "time"

// SourceDMHY is the stable DMHY source identifier. It is inlined here (the leaf
// wire contract) so the takuhai service and its tests never import the foreign
// sources/dmhy crawler module just to name the source.
const SourceDMHY = "dmhy"

// RawPost is one crawled post: the POST /crawl-response ↔ POST /ingest-request
// shape. The crawler is DUMB — it emits raw fields (title, magnet, metadata,
// size) and does NOT normalize the infohash; takuhai derives the canonical dedup
// key from Magnet on /ingest (via internal/infohash). There is deliberately NO
// infohash field here.
//
// Field names/types mirror store.IngestParams (the persistence input) so the P4
// /ingest mapping is a trivial field copy, but this leaf imports neither store
// nor the source layer.
type RawPost struct {
	Title       string    `json:"title"`        // raw, unparsed
	Magnet      string    `json:"magnet"`       // representative seed; takuhai derives the infohash from this
	Source      string    `json:"source"`       // e.g. "dmhy"
	SourceID    string    `json:"source_id"`    // source-native stable id (DMHY GUID) — unique with Source
	URL         string    `json:"url"`          //
	PublishedAt time.Time `json:"published_at"` //
	SizeBytes   int64     `json:"size_bytes"`   // parsed total size in bytes; 0 = unset
}

// QueueStats mirrors the small queue wake signal returned by /ingest.
type QueueStats struct {
	Available int64 `json:"available"`
	Leased    int64 `json:"leased"`
	Exhausted int64 `json:"exhausted"`
}

// IngestBatch is the per-call breakdown of one POST /ingest: how each post in the
// posted batch was resolved. skipped is owned by the REST ingest handler (magnet yields no
// canonical v1 btih — pure-v2/malformed); the other four come from the store seam
// (queued = first-seen infohash; recheck = new evidence on a known infohash;
// duplicate = same (source, source_id), nothing linked; conflict = reused
// (source, source_id) with a NEW infohash, rejected as an orphan-release no-op).
type IngestBatch struct {
	New       int `json:"new"`
	Updated   int `json:"updated"`
	Duplicate int `json:"duplicate"`
	Conflict  int `json:"conflict"`
	Skipped   int `json:"skipped"`
}

// IngestSummary is the POST /ingest response: the per-call Batch buckets PLUS the
// current durable Queue counts. n8n wakes the matcher off queue.available, not the
// Batch buckets, so the wake signal survives a lost response.
type IngestSummary struct {
	Batch IngestBatch `json:"batch"`
	Queue QueueStats  `json:"queue"`
}
