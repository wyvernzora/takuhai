package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wyvernzora/takuhai/internal/store"
)

type fakeStore struct {
	claim           store.ClaimParams
	submit          store.SubmitParams
	infohashes      []string
	releaseInfohash string
}

func (f *fakeStore) Ping(context.Context) error { return nil }
func (f *fakeStore) IngestN(context.Context, store.IngestParams) (store.IngestOutcome, error) {
	return store.IngestOutcome{}, nil
}
func (f *fakeStore) Claim(_ context.Context, p store.ClaimParams) (store.ClaimResult, error) {
	f.claim = p
	return store.ClaimResult{
		Items: []store.ClaimedRelease{{
			Infohash:     "0123456789abcdef0123456789abcdef01234567",
			ClaimToken:   1,
			AttemptCount: 1,
			LeaseExpires: time.Unix(1, 0).UTC(),
		}},
	}, nil
}
func (f *fakeStore) Submit(_ context.Context, p store.SubmitParams) error {
	f.submit = p
	return nil
}
func (f *fakeStore) QueueStats(context.Context) (store.QueueStats, error) {
	return store.QueueStats{}, nil
}
func (f *fakeStore) CatalogStats(context.Context) (store.CatalogStats, error) {
	return store.CatalogStats{}, nil
}
func (f *fakeStore) ListReleases(context.Context, store.ReleaseQuery) (store.ReleasePage, error) {
	return store.ReleasePage{}, nil
}
func (f *fakeStore) GetRelease(_ context.Context, infohash string) (store.ReleaseDetail, error) {
	f.releaseInfohash = infohash
	return store.ReleaseDetail{
		Infohash:    infohash,
		Title:       "release",
		PublishedAt: time.Unix(1, 0).UTC(),
		MatchStatus: "unmatched",
		CreatedAt:   time.Unix(1, 0).UTC(),
		UpdatedAt:   time.Unix(1, 0).UTC(),
	}, nil
}
func (f *fakeStore) ResolveMagnets(_ context.Context, infohashes []string) (map[string]string, error) {
	f.infohashes = infohashes
	return map[string]string{
		"0123456789abcdef0123456789abcdef01234567": "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=udp://tracker",
	}, nil
}
func (f *fakeStore) Close() error { return nil }

func TestClaimRejectsInvalidJSON(t *testing.T) {
	st := &fakeStore{}
	for _, body := range []string{"", "{"} {
		req := httptest.NewRequest(http.MethodPost, "/queue/claim", strings.NewReader(body))
		rec := httptest.NewRecorder()

		New(st).ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want %d; response %s", body, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	}
}

func TestSubmitRejectsInvalidStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(`{
		"infohash":"0123456789abcdef0123456789abcdef01234567",
		"claim_token":1,
		"status":"defer"
	}`))
	rec := httptest.NewRecorder()

	New(&fakeStore{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; response %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestSubmitPreservesSuppressedConfidence(t *testing.T) {
	st := &fakeStore{}
	req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(`{
		"infohash":"0123456789abcdef0123456789abcdef01234567",
		"claim_token":1,
		"status":"suppressed",
		"confidence":0.73
	}`))
	rec := httptest.NewRecorder()

	New(st).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; response %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if st.submit.Confidence == nil || *st.submit.Confidence != 0.73 {
		t.Fatalf("confidence = %v, want 0.73", st.submit.Confidence)
	}
}

func TestGetMagnet(t *testing.T) {
	st := &fakeStore{}
	req := httptest.NewRequest(http.MethodGet, "/magnets/0123456789abcdef0123456789abcdef01234567", http.NoBody)
	rec := httptest.NewRecorder()

	New(st).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; response %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(st.infohashes) != 1 || st.infohashes[0] != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("infohashes = %#v", st.infohashes)
	}
	var body struct {
		Infohash string `json:"infohash"`
		Magnet   string `json:"magnet"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Infohash != "0123456789abcdef0123456789abcdef01234567" || body.Magnet != "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=udp://tracker" {
		t.Fatalf("response = %+v", body)
	}
}

func TestGetRelease(t *testing.T) {
	st := &fakeStore{}
	req := httptest.NewRequest(http.MethodGet, "/releases/JN7DUBILGVIFL46NCSOF42GZ5JCXNNUS", http.NoBody)
	rec := httptest.NewRecorder()

	New(st).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; response %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if st.releaseInfohash != "4b7e3a050b355055f3cd149c5e68d9ea4576b692" {
		t.Fatalf("release infohash = %q, want canonical hex", st.releaseInfohash)
	}
	var body struct {
		Infohash    string `json:"infohash"`
		Title       string `json:"title"`
		MatchStatus string `json:"match_status"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Infohash != "4b7e3a050b355055f3cd149c5e68d9ea4576b692" || body.Title != "release" || body.MatchStatus != "unmatched" {
		t.Fatalf("response = %+v", body)
	}
}

func TestGetReleaseRejectsInvalidInfohash(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/releases/not-an-infohash", http.NoBody)
	rec := httptest.NewRecorder()

	New(&fakeStore{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; response %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "invalid_input" || body.Message != "invalid infohash" {
		t.Fatalf("response = %+v", body)
	}
}
