//go:build conformance

package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wyvernzora/takuhai/internal/dispatch"
	takuhaimcp "github.com/wyvernzora/takuhai/internal/mcp"
	"github.com/wyvernzora/takuhai/internal/rest"
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
	apiIH3 = "fedcbafedcbafedcbafedcbafedcbafedcbafedc"
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
	clock.Advance(time.Minute)
	seedRelease(t, ctx, st, apiIH2, "api-2", clock.now)
	second := claimOne(t, ctx, st, 60)
	if err := st.Submit(ctx, store.SubmitParams{
		Infohash:   apiIH2,
		ClaimToken: second.ClaimToken,
		Status:     "matched",
		Ref:        "tvdb:999",
		Confidence: ptr(0.81),
		Reason:     "title matches other ref",
	}); err != nil {
		t.Fatalf("Submit second matched: %v", err)
	}

	d := dispatch.New(st)
	res, err := d.ListReleases(ctx, mustJSON(t, map[string]any{"ref": "tvdb:123"}))
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	var listed struct {
		Releases []struct {
			Infohash   string  `json:"infohash"`
			Ref        string  `json:"ref"`
			Confidence float64 `json:"confidence"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(res, &listed); err != nil {
		t.Fatalf("decode list_releases: %v", err)
	}
	if len(listed.Releases) != 1 || listed.Releases[0].Infohash != apiIH1 {
		t.Fatalf("list_releases = %+v, want %s", listed.Releases, apiIH1)
	}
	if listed.Releases[0].Ref != "tvdb:123" {
		t.Fatalf("ref = %q, want tvdb:123", listed.Releases[0].Ref)
	}
	if listed.Releases[0].Confidence != 0.94 {
		t.Fatalf("confidence = %v, want 0.94", listed.Releases[0].Confidence)
	}

	res, err = d.ListReleases(ctx, mustJSON(t, map[string]any{}))
	if err != nil {
		t.Fatalf("ListReleases unfiltered: %v", err)
	}
	listed.Releases = nil
	if err := json.Unmarshal(res, &listed); err != nil {
		t.Fatalf("decode unfiltered list_releases: %v", err)
	}
	if len(listed.Releases) != 2 || listed.Releases[0].Infohash != apiIH2 || listed.Releases[1].Infohash != apiIH1 {
		t.Fatalf("unfiltered list_releases = %+v, want newest %s then %s", listed.Releases, apiIH2, apiIH1)
	}
	if listed.Releases[0].Ref != "tvdb:999" || listed.Releases[1].Ref != "tvdb:123" {
		t.Fatalf("unfiltered refs = %+v, want tvdb:999 then tvdb:123", listed.Releases)
	}

	magRes, err := d.ResolveMagnets(ctx, mustJSON(t, map[string]any{"infohashes": []string{apiIH1, apiIH3}}))
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
	if _, ok := resolved.Magnets[apiIH3]; ok {
		t.Fatalf("resolve_magnets included unknown infohash %s", apiIH3)
	}
}

func TestAPIShape_GetReleaseDetail(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	st := newConformanceStoreWithClock(t, clock)

	seedReleaseFrom(t, ctx, st, apiIH1, "dmhy", "api-detail-1", clock.now)
	seedReleaseFrom(t, ctx, st, apiIH1, "nyaa", "api-detail-2", clock.now.Add(time.Minute))

	first := claimOne(t, ctx, st, 60)
	if err := st.Submit(ctx, store.SubmitParams{
		Infohash:   apiIH1,
		ClaimToken: first.ClaimToken,
		Status:     "unmatched",
		Reason:     "needs another pass",
	}); err != nil {
		t.Fatalf("Submit unmatched: %v", err)
	}
	clock.Advance(61 * time.Second)
	second := claimOne(t, ctx, st, 60)
	if err := st.Submit(ctx, store.SubmitParams{
		Infohash:   apiIH1,
		ClaimToken: second.ClaimToken,
		Status:     "matched",
		Ref:        "tvdb:123",
		Confidence: ptr(0.94),
		Reason:     "title matches",
	}); err != nil {
		t.Fatalf("Submit matched: %v", err)
	}

	d := dispatch.New(st)
	res, err := d.GetRelease(ctx, mustJSON(t, map[string]any{"infohash": apiIH1}))
	if err != nil {
		t.Fatalf("GetRelease dispatch: %v", err)
	}
	detail := decodeReleaseDetail(t, res)
	assertReleaseDetail(t, detail)

	req := httptest.NewRequest(http.MethodGet, "/releases/"+apiIH1, http.NoBody)
	rec := httptest.NewRecorder()
	rest.New(st).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /releases/{infohash} = %d, want 200; response %s", rec.Code, rec.Body.String())
	}
	assertReleaseDetail(t, decodeReleaseDetail(t, rec.Body.Bytes()))
}

func TestAPIShape_GetReleaseExplicitNullsAndNoLeaseInternals(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{now: time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)}
	st := newConformanceStoreWithClock(t, clock)

	_, err := st.IngestN(ctx, store.IngestParams{
		Infohash:    apiIH1,
		Source:      "dmhy",
		SourceID:    "api-null-1",
		Title:       "raw release api-null-1",
		PublishedAt: clock.now,
	})
	if err != nil {
		t.Fatalf("IngestN minimal release: %v", err)
	}
	claimed := claimOne(t, ctx, st, 60)
	if err := st.Submit(ctx, store.SubmitParams{
		Infohash:   apiIH1,
		ClaimToken: claimed.ClaimToken,
		Status:     "unmatched",
	}); err != nil {
		t.Fatalf("Submit unmatched without facts: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/releases/"+apiIH1, http.NoBody)
	rec := httptest.NewRecorder()
	rest.New(st).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /releases/{infohash} = %d, want 200; response %s", rec.Code, rec.Body.String())
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode release detail as raw JSON: %v", err)
	}
	for _, key := range []string{"magnet", "size_bytes", "ref", "confidence", "first_matched_at"} {
		assertJSONNull(t, body, key)
	}
	for _, key := range []string{"raw_items", "match_events"} {
		assertJSONArray(t, body, key)
	}
	for _, key := range []string{"claim_token", "claimed_at", "lease_expires_at"} {
		if _, ok := body[key]; ok {
			t.Fatalf("response included lease-internal key %q", key)
		}
	}

	var rawItems []map[string]json.RawMessage
	if err := json.Unmarshal(body["raw_items"], &rawItems); err != nil {
		t.Fatalf("decode raw_items: %v", err)
	}
	if len(rawItems) != 1 {
		t.Fatalf("raw_items len = %d, want 1", len(rawItems))
	}
	assertJSONNull(t, rawItems[0], "url")

	var events []map[string]json.RawMessage
	if err := json.Unmarshal(body["match_events"], &events); err != nil {
		t.Fatalf("decode match_events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("match_events len = %d, want 1", len(events))
	}
	for _, key := range []string{"ref", "confidence", "reason"} {
		assertJSONNull(t, events[0], key)
	}
}

func TestAPIShape_GetReleaseRESTErrors(t *testing.T) {
	ctx := context.Background()
	st := newConformanceStore(t)
	handler := rest.New(st)

	for _, tt := range []struct {
		name       string
		path       string
		wantStatus int
		wantCode   string
	}{
		{name: "invalid infohash", path: "/releases/not-an-infohash", wantStatus: http.StatusBadRequest, wantCode: "invalid_input"},
		{name: "unknown release", path: "/releases/" + apiIH3, wantStatus: http.StatusNotFound, wantCode: "no_such_release"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, http.NoBody).WithContext(ctx)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("%s = %d, want %d; response %s", tt.path, rec.Code, tt.wantStatus, rec.Body.String())
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if body.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", body.Code, tt.wantCode)
			}
		})
	}
}

func TestAPIShape_GetReleaseMCPNoSuchRelease(t *testing.T) {
	ctx := context.Background()
	st := newConformanceStore(t)
	httpSrv := httptest.NewServer(takuhaimcp.NewServer(st, nil).Handler())
	t.Cleanup(httpSrv.Close)

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "conformance-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: httpSrv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("MCP connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "get_release",
		Arguments: map[string]any{"infohash": apiIH3},
	})
	if err != nil {
		t.Fatalf("MCP get_release: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError = false, content = %v", res.Content)
	}
	if got := firstMCPText(res); !strings.Contains(got, `"code":"no_such_release"`) {
		t.Fatalf("error content = %s, want no_such_release code", got)
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
	seedReleaseFrom(t, ctx, st, ih, "dmhy", sourceID, published)
}

func seedReleaseFrom(t *testing.T, ctx context.Context, st store.Store, ih, source, sourceID string, published time.Time) {
	t.Helper()
	_, err := st.IngestN(ctx, store.IngestParams{
		Infohash:    ih,
		Source:      source,
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

type releaseDetailBody struct {
	Infohash       string   `json:"infohash"`
	Magnet         *string  `json:"magnet"`
	MatchStatus    string   `json:"match_status"`
	Ref            *string  `json:"ref"`
	Confidence     *float64 `json:"confidence"`
	FirstMatchedAt *string  `json:"first_matched_at"`
	RawItems       []struct {
		ID       int64  `json:"id"`
		Source   string `json:"source"`
		SourceID string `json:"source_id"`
	} `json:"raw_items"`
	MatchEvents []struct {
		ID        int64   `json:"id"`
		Status    string  `json:"status"`
		Ref       *string `json:"ref"`
		Reason    *string `json:"reason"`
		CreatedAt string  `json:"created_at"`
	} `json:"match_events"`
}

func decodeReleaseDetail(t *testing.T, b []byte) releaseDetailBody {
	t.Helper()
	var detail releaseDetailBody
	if err := json.Unmarshal(b, &detail); err != nil {
		t.Fatalf("decode release detail: %v", err)
	}
	return detail
}

func assertReleaseDetail(t *testing.T, detail releaseDetailBody) {
	t.Helper()
	if detail.Infohash != apiIH1 {
		t.Fatalf("infohash = %q, want %q", detail.Infohash, apiIH1)
	}
	if detail.Magnet == nil || !strings.Contains(*detail.Magnet, "&tr=") {
		t.Fatalf("magnet = %v, want stored full magnet", detail.Magnet)
	}
	if detail.MatchStatus != "matched" || detail.Ref == nil || *detail.Ref != "tvdb:123" {
		t.Fatalf("match state = status %q ref %v, want matched tvdb:123", detail.MatchStatus, detail.Ref)
	}
	if detail.Confidence == nil || *detail.Confidence != 0.94 {
		t.Fatalf("confidence = %v, want 0.94", detail.Confidence)
	}
	if detail.FirstMatchedAt == nil {
		t.Fatal("first_matched_at = nil, want timestamp")
	}
	if len(detail.RawItems) != 2 {
		t.Fatalf("raw_items len = %d, want 2", len(detail.RawItems))
	}
	if detail.RawItems[0].ID >= detail.RawItems[1].ID {
		t.Fatalf("raw_items order = %d then %d, want id asc", detail.RawItems[0].ID, detail.RawItems[1].ID)
	}
	if detail.RawItems[0].SourceID != "api-detail-1" || detail.RawItems[1].SourceID != "api-detail-2" {
		t.Fatalf("raw_items = %+v, want insertion order by id", detail.RawItems)
	}
	if len(detail.MatchEvents) != 2 {
		t.Fatalf("match_events len = %d, want 2", len(detail.MatchEvents))
	}
	if detail.MatchEvents[0].Status != "unmatched" || detail.MatchEvents[1].Status != "matched" {
		t.Fatalf("match_events statuses = %+v, want chronological unmatched then matched", detail.MatchEvents)
	}
	firstCreatedAt, err := time.Parse(time.RFC3339Nano, detail.MatchEvents[0].CreatedAt)
	if err != nil {
		t.Fatalf("match_events[0].created_at = %q, want RFC3339Nano: %v", detail.MatchEvents[0].CreatedAt, err)
	}
	secondCreatedAt, err := time.Parse(time.RFC3339Nano, detail.MatchEvents[1].CreatedAt)
	if err != nil {
		t.Fatalf("match_events[1].created_at = %q, want RFC3339Nano: %v", detail.MatchEvents[1].CreatedAt, err)
	}
	if firstCreatedAt.After(secondCreatedAt) {
		t.Fatalf("match_events created_at order = %q then %q, want asc", detail.MatchEvents[0].CreatedAt, detail.MatchEvents[1].CreatedAt)
	}
}

func assertJSONNull(t *testing.T, obj map[string]json.RawMessage, key string) {
	t.Helper()
	got, ok := obj[key]
	if !ok {
		t.Fatalf("missing JSON key %q", key)
	}
	if string(got) != "null" {
		t.Fatalf("%s = %s, want null", key, got)
	}
}

func assertJSONArray(t *testing.T, obj map[string]json.RawMessage, key string) {
	t.Helper()
	got, ok := obj[key]
	if !ok {
		t.Fatalf("missing JSON key %q", key)
	}
	if len(got) == 0 || got[0] != '[' {
		t.Fatalf("%s = %s, want JSON array", key, got)
	}
}

func firstMCPText(res *mcpsdk.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			return tc.Text
		}
	}
	return ""
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
