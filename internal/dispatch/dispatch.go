package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wyvernzora/takuhai/internal/cursor"
	"github.com/wyvernzora/takuhai/internal/store"
)

var ErrInvalidInput = errors.New("takuhai/dispatch: invalid input")

type Dispatcher struct {
	store store.Store
}

func New(s store.Store) *Dispatcher { return &Dispatcher{store: s} }

type ClaimRequest struct {
	Limit        int `json:"limit"`
	LeaseSeconds int `json:"lease_seconds"`
}

type ClaimItemResult struct {
	Infohash       string          `json:"infohash"`
	ClaimToken     int64           `json:"claim_token"`
	AttemptCount   int             `json:"attempt_count"`
	LeaseExpiresAt string          `json:"lease_expires_at"`
	RawItems       []RawItemResult `json:"raw_items"`
}

type RawItemResult struct {
	ID          int64  `json:"id"`
	Source      string `json:"source"`
	SourceID    string `json:"source_id"`
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	PublishedAt string `json:"published_at"`
}

type ClaimResult struct {
	Items []ClaimItemResult `json:"items"`
}

type SubmitRequest struct {
	Infohash   string   `json:"infohash"`
	ClaimToken int64    `json:"claim_token"`
	Status     string   `json:"status"`
	Ref        string   `json:"ref,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

type QueueStatsResult struct {
	Available  int `json:"available"`
	Leased     int `json:"leased"`
	Unmatched  int `json:"unmatched"`
	Matched    int `json:"matched"`
	Suppressed int `json:"suppressed"`
	Exhausted  int `json:"exhausted"`
}

type ListReleasesRequest struct {
	Ref    string     `json:"ref,omitempty" jsonschema:"optional opaque metadata ref in namespace:value form; omit to list recent matched releases across all refs"`
	Since  *time.Time `json:"since,omitempty" jsonschema:"RFC3339 timestamp; when present, page by first matched time"`
	Limit  int        `json:"limit,omitempty" jsonschema:"maximum releases to return; server defaults and caps apply"`
	Cursor string     `json:"cursor,omitempty" jsonschema:"opaque next_cursor from the previous response"`
}

type ReleaseItemResult struct {
	Infohash    string   `json:"infohash"`
	Ref         string   `json:"ref"`
	Title       string   `json:"title"`
	SizeBytes   int64    `json:"size_bytes"`
	PublishedAt string   `json:"published_at"`
	Confidence  float64  `json:"confidence"`
	Sources     []string `json:"sources"`
}

type ListReleasesResult struct {
	Releases   []ReleaseItemResult `json:"releases"`
	NextCursor *string             `json:"next_cursor,omitempty"`
}

type ResolveMagnetsRequest struct {
	Infohashes []string `json:"infohashes"`
}

type ResolveMagnetsResult struct {
	Magnets map[string]string `json:"magnets"`
}

func (d *Dispatcher) Claim(ctx context.Context, input []byte) ([]byte, error) {
	var req ClaimRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, err
	}
	out, err := d.ClaimTyped(ctx, req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (d *Dispatcher) ClaimTyped(ctx context.Context, req ClaimRequest) (ClaimResult, error) {
	res, err := d.store.Claim(ctx, store.ClaimParams{
		Limit:        req.Limit,
		LeaseSeconds: req.LeaseSeconds,
	})
	if err != nil {
		return ClaimResult{}, err
	}
	out := ClaimResult{Items: make([]ClaimItemResult, 0, len(res.Items))}
	for _, it := range res.Items {
		raw := make([]RawItemResult, 0, len(it.RawItems))
		for _, ri := range it.RawItems {
			raw = append(raw, RawItemResult{
				ID:          ri.ID,
				Source:      ri.Source,
				SourceID:    ri.SourceID,
				Title:       ri.Title,
				URL:         ri.URL,
				PublishedAt: ri.PublishedAt.Format(time.RFC3339Nano),
			})
		}
		out.Items = append(out.Items, ClaimItemResult{
			Infohash:       it.Infohash,
			ClaimToken:     it.ClaimToken,
			AttemptCount:   it.AttemptCount,
			LeaseExpiresAt: it.LeaseExpires.Format(time.RFC3339Nano),
			RawItems:       raw,
		})
	}
	return out, nil
}

func (d *Dispatcher) Submit(ctx context.Context, input []byte) ([]byte, error) {
	var req SubmitRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, err
	}
	if err := d.SubmitTyped(ctx, req); err != nil {
		return nil, err
	}
	return []byte(`{"ok":true}`), nil
}

func (d *Dispatcher) SubmitTyped(ctx context.Context, req SubmitRequest) error {
	switch req.Status {
	case "matched", "unmatched", "suppressed":
	default:
		return fmt.Errorf("%w: invalid status %q", ErrInvalidInput, req.Status)
	}
	if err := d.store.Submit(ctx, store.SubmitParams{
		Infohash:   req.Infohash,
		ClaimToken: req.ClaimToken,
		Status:     req.Status,
		Ref:        req.Ref,
		Confidence: req.Confidence,
		Reason:     req.Reason,
	}); err != nil {
		return err
	}
	return nil
}

func (d *Dispatcher) QueueStats(ctx context.Context, input []byte) ([]byte, error) {
	out, err := d.QueueStatsTyped(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (d *Dispatcher) QueueStatsTyped(ctx context.Context) (QueueStatsResult, error) {
	qs, err := d.store.QueueStats(ctx)
	if err != nil {
		return QueueStatsResult{}, err
	}
	return QueueStatsResult{
		Available:  qs.Available,
		Leased:     qs.Leased,
		Unmatched:  qs.Unmatched,
		Matched:    qs.Matched,
		Suppressed: qs.Suppressed,
		Exhausted:  qs.Exhausted,
	}, nil
}

func (d *Dispatcher) ListReleases(ctx context.Context, input []byte) ([]byte, error) {
	var req ListReleasesRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, err
	}
	out, err := d.ListReleasesTyped(ctx, req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (d *Dispatcher) ListReleasesTyped(ctx context.Context, req ListReleasesRequest) (ListReleasesResult, error) {
	page, err := d.store.ListReleases(ctx, store.ReleaseQuery{
		Ref:    req.Ref,
		Since:  req.Since,
		Limit:  req.Limit,
		Cursor: req.Cursor,
	})
	if err != nil {
		return ListReleasesResult{}, err
	}
	out := ListReleasesResult{Releases: make([]ReleaseItemResult, 0, len(page.Releases))}
	for _, r := range page.Releases {
		out.Releases = append(out.Releases, ReleaseItemResult{
			Infohash:    r.Infohash,
			Ref:         r.Ref,
			Title:       r.Title,
			SizeBytes:   r.SizeBytes,
			PublishedAt: r.PublishedAt.Format(time.RFC3339Nano),
			Confidence:  r.Confidence,
			Sources:     r.Sources,
		})
	}
	if page.NextCursor != "" {
		nc := page.NextCursor
		out.NextCursor = &nc
	}
	return out, nil
}

func (d *Dispatcher) ResolveMagnets(ctx context.Context, input []byte) ([]byte, error) {
	var req ResolveMagnetsRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, err
	}
	out, err := d.ResolveMagnetsTyped(ctx, req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (d *Dispatcher) ResolveMagnetsTyped(ctx context.Context, req ResolveMagnetsRequest) (ResolveMagnetsResult, error) {
	magnets, err := d.store.ResolveMagnets(ctx, req.Infohashes)
	if err != nil {
		return ResolveMagnetsResult{}, err
	}
	if magnets == nil {
		magnets = map[string]string{}
	}
	return ResolveMagnetsResult{Magnets: magnets}, nil
}

func WireCode(err error) string {
	switch {
	case errors.Is(err, store.ErrNoSuchRelease):
		return "no_such_release"
	case errors.Is(err, store.ErrNoActiveLease):
		return "no_active_lease"
	case errors.Is(err, store.ErrStaleLease):
		return "stale_lease"
	case errors.Is(err, cursor.ErrInvalidRef):
		return "invalid_ref"
	case errors.Is(err, cursor.ErrInvalidCursor):
		return "invalid_cursor"
	case errors.Is(err, ErrInvalidInput):
		return "invalid_input"
	default:
		return ""
	}
}
