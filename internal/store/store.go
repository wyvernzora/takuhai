package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNoSuchRelease = errors.New("takuhai: no such release")
	ErrNoActiveLease = errors.New("takuhai: no active lease")
	ErrStaleLease    = errors.New("takuhai: stale lease")
)

type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

type Store interface {
	Ping(ctx context.Context) error
	IngestN(ctx context.Context, p IngestParams) (IngestOutcome, error)
	Claim(ctx context.Context, p ClaimParams) (ClaimResult, error)
	Submit(ctx context.Context, p SubmitParams) error
	QueueStats(ctx context.Context) (QueueStats, error)
	ListReleases(ctx context.Context, q ReleaseQuery) (ReleasePage, error)
	ResolveMagnets(ctx context.Context, infohashes []string) (map[string]string, error)
	Close() error
}

type IngestParams struct {
	Infohash    string
	Source      string
	SourceID    string
	Title       string
	URL         string
	Magnet      string
	SizeBytes   int64
	PublishedAt time.Time
}

type IngestOutcome struct {
	New       bool
	Updated   bool
	Duplicate bool
	Conflict  bool
}

type ClaimParams struct {
	Limit        int
	LeaseSeconds int
}

type ClaimResult struct {
	Items []ClaimedRelease
}

type ClaimedRelease struct {
	Infohash     string
	ClaimToken   int64
	AttemptCount int
	LeaseExpires time.Time
	RawItems     []RawItemEvent
}

type RawItemEvent struct {
	ID          int64
	Source      string
	SourceID    string
	Title       string
	URL         string
	PublishedAt time.Time
}

type SubmitParams struct {
	Infohash   string
	ClaimToken int64
	Status     string
	Ref        string
	Confidence float64
	Reason     string
}

type QueueStats struct {
	Available  int
	Leased     int
	Unmatched  int
	Matched    int
	Suppressed int
	Exhausted  int
}

type ReleaseQuery struct {
	Ref    string
	Since  *time.Time
	Limit  int
	Cursor string
}

type ReleasePage struct {
	Releases   []ReleaseItem
	NextCursor string
}

type ReleaseItem struct {
	Infohash    string
	Title       string
	SizeBytes   int64
	PublishedAt time.Time
	Confidence  float64
	Sources     []string
}
