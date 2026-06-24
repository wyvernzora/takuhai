package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wyvernzora/takuhai/internal/store"
)

type fakeStore struct {
	claim  store.ClaimParams
	submit store.SubmitParams
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
func (f *fakeStore) ListReleases(context.Context, store.ReleaseQuery) (store.ReleasePage, error) {
	return store.ReleasePage{}, nil
}
func (f *fakeStore) ResolveMagnets(context.Context, []string) (map[string]string, error) {
	return nil, nil
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
