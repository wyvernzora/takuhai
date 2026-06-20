// Package store is the Postgres persistence layer over pgx. It owns the
// three tables — raw_items (immutable provenance), releases (deduped,
// queryable, matchable), and match_events (append-only audit) — and the
// durable work queue, claimed via SELECT ... FOR UPDATE SKIP LOCKED.
//
// Implementation lands in Phase 1; see docs/indexer-handover.md §6.
package store
