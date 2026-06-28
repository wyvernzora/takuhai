//go:build conformance

package conformance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wyvernzora/takuhai/internal/dispatch"
	"github.com/wyvernzora/takuhai/internal/store"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

const (
	apiIH1 = "0123456789abcdef0123456789abcdef01234567"
	apiIH2 = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
)

func TestAPIShape_MatchListsReleaseAndResolvesMagnet(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	st := newConformanceStoreWithClock(t, clock)

	seedRelease(t, ctx, st, apiIH1, "api-1", clock.now)
	claim, err := st.Claim(ctx, store.ClaimParams{LeaseSeconds: 60})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claim.Items) != 1 {
		t.Fatalf("Claim returned %d items, want 1", len(claim.Items))
	}
	if claim.Items[0].ClaimToken == 0 {
		t.Fatal("ClaimToken = 0, want non-zero fencing token")
	}

	if err := st.Submit(ctx, store.SubmitParams{
		Infohash:   apiIH1,
		ClaimToken: claim.Items[0].ClaimToken,
		Status:     "matched",
		Ref:        "tvdb:123",
		Confidence: ptr(0.94),
		Reason:     "title matches",
	}); err != nil {
		t.Fatalf("Submit matched: %v", err)
	}

	d := dispatch.New(st)
	res, err := d.ListReleases(ctx, mustJSON(t, map[string]any{"ref": "tvdb:123"}))
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	var listed struct {
		Releases []struct {
			Infohash   string  `json:"infohash"`
			Confidence float64 `json:"confidence"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(res, &listed); err != nil {
		t.Fatalf("decode list_releases: %v", err)
	}
	if len(listed.Releases) != 1 || listed.Releases[0].Infohash != apiIH1 {
		t.Fatalf("list_releases = %+v, want %s", listed.Releases, apiIH1)
	}
	if listed.Releases[0].Confidence != 0.94 {
		t.Fatalf("confidence = %v, want 0.94", listed.Releases[0].Confidence)
	}

	magRes, err := d.ResolveMagnets(ctx, mustJSON(t, map[string]any{"infohashes": []string{apiIH1, apiIH2}}))
	if err != nil {
		t.Fatalf("ResolveMagnets: %v", err)
	}
	var resolved struct {
		Magnets map[string]string `json:"magnets"`
	}
	if err := json.Unmarshal(magRes, &resolved); err != nil {
		t.Fatalf("decode resolve_magnets: %v", err)
	}
	got := resolved.Magnets[apiIH1]
	if got == "" || !strings.Contains(got, "&tr=") {
		t.Fatalf("resolved magnet = %q, want stored full magnet with trackers", got)
	}
	if _, ok := resolved.Magnets[apiIH2]; ok {
		t.Fatalf("resolve_magnets included unknown infohash %s", apiIH2)
	}
}

func TestAPIShape_StaleClaimTokenRejected(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	st := newConformanceStoreWithClock(t, clock)
	seedRelease(t, ctx, st, apiIH1, "api-stale", clock.now)

	first := claimOne(t, ctx, st, 60)
	clock.Advance(61 * time.Second)
	second := claimOne(t, ctx, st, 60)
	if first.ClaimToken == second.ClaimToken {
		t.Fatalf("claim token did not change: %d", first.ClaimToken)
	}
	if first.AttemptCount != 0 || second.AttemptCount != 0 {
		t.Fatalf("claim crash changed attempt counts: first=%d second=%d, want both 0", first.AttemptCount, second.AttemptCount)
	}
	err := st.Submit(ctx, store.SubmitParams{
		Infohash:   apiIH1,
		ClaimToken: first.ClaimToken,
		Status:     "matched",
		Ref:        "tvdb:123",
		Confidence: ptr(0.9),
	})
	if err == nil || !strings.Contains(err.Error(), store.ErrStaleLease.Error()) {
		t.Fatalf("Submit with stale token err = %v, want stale lease", err)
	}
}

func TestAPIShape_UnmatchedExhaustsAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	st := newConformanceStoreWithClock(t, clock)
	seedRelease(t, ctx, st, apiIH1, "api-exhaust", clock.now)

	for i := 0; i < 3; i++ {
		claimed := claimOne(t, ctx, st, 60)
		if claimed.AttemptCount != i {
			t.Fatalf("claim attempt_count before submit = %d, want %d", claimed.AttemptCount, i)
		}
		if err := st.Submit(ctx, store.SubmitParams{
			Infohash:   apiIH1,
			ClaimToken: claimed.ClaimToken,
			Status:     "unmatched",
			Reason:     "no match",
		}); err != nil {
			t.Fatalf("Submit unmatched attempt %d: %v", i+1, err)
		}
		clock.Advance(61 * time.Second)
	}
	qs, err := st.QueueStats(ctx)
	if err != nil {
		t.Fatalf("QueueStats: %v", err)
	}
	if qs.Exhausted != 1 || qs.Available != 0 {
		t.Fatalf("QueueStats after exhaustion = %+v, want exhausted=1 available=0", qs)
	}
}

func seedRelease(t *testing.T, ctx context.Context, st store.Store, ih, sourceID string, published time.Time) {
	t.Helper()
	_, err := st.IngestN(ctx, store.IngestParams{
		Infohash:    ih,
		Source:      "dmhy",
		SourceID:    sourceID,
		Title:       "raw release " + sourceID,
		URL:         "https://example.invalid/" + sourceID,
		Magnet:      "magnet:?xt=urn:btih:" + ih + "&tr=udp%3A%2F%2Ftracker.example%3A80%2Fannounce",
		SizeBytes:   1234,
		PublishedAt: published,
	})
	if err != nil {
		t.Fatalf("IngestN: %v", err)
	}
}

func claimOne(t *testing.T, ctx context.Context, st store.Store, leaseSeconds int) store.ClaimedRelease {
	t.Helper()
	res, err := st.Claim(ctx, store.ClaimParams{LeaseSeconds: leaseSeconds})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("Claim returned %d items, want 1", len(res.Items))
	}
	return res.Items[0]
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return b
}

func ptr[T any](v T) *T { return &v }
