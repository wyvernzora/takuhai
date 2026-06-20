// Package source defines the pluggable ingestion abstraction. DMHY is the
// only adapter in v1, but the interface and the normalized RawItem it
// produces are multi-source from day one: adding nyaa later must not touch
// the ingestion core.
package source

import (
	"context"
	"encoding/json"
	"time"
)

// Source is a pluggable ingestion source.
type Source interface {
	// Name returns the stable source identifier, e.g. "dmhy".
	Name() string

	// Fetch returns a page of raw items at/after the cursor, plus the next
	// cursor. The cursor is opaque and source-defined (live RSS has none;
	// an HTML backfill encodes a page). An empty next cursor signals
	// end-of-stream for backfill.
	Fetch(ctx context.Context, cursor string) (items []RawItem, next string, err error)
}

// RawItem is the normalized output every adapter produces. It maps onto a
// raw_items row; the ingestion core dedups items into releases by infohash.
type RawItem struct {
	Source      string
	SourceID    string          // source-native stable id (e.g. DMHY GUID)
	URL         string          //
	Title       string          // raw, unparsed
	Infohash    string          // normalized lowercase btih, "" if unknown
	Magnet      string          //
	Author      string          // source-provided subgroup string, raw
	CategoryID  int             //
	Category    string          //
	PublishedAt time.Time       //
	Raw         json.RawMessage // full original payload, for replay/forensics
}
