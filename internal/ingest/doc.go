// Package ingest is the source-agnostic ingestion core. For each RawItem an
// adapter yields it computes the dedup key (infohash when present, else
// "<source>:<source_id>"), idempotently upserts the releases row, links an
// immutable raw_items record, and enqueues genuinely new releases as
// unmatched. It performs no matching.
//
// Implementation lands in Phase 1; see docs/indexer-handover.md §7.
package ingest
