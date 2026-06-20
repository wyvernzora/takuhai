// Package dmhy implements the DMHY (動漫花園) ingestion source: live RSS for
// recent items and a bounded HTML-archive crawl for backfill, both feeding
// the same ingestion path. It wraps the rate-limited dmhy-mcp client.
//
// Implementation lands in Phase 1 (live) and Phase 4 (backfill); see
// docs/indexer-handover.md §8 and §13.
package dmhy
