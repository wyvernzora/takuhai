package metrics

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/wyvernzora/takuhai/internal/store"
)

func TestHTTPWrapRecordsRouteAndStatus(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newHTTP(reg, "testapp", map[string]string{"/known": "/known", "/magnets/": "/magnets/{infohash}"})
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/known", http.NoBody))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/magnets/0123456789abcdef0123456789abcdef01234567", http.NoBody))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/random/123", http.NoBody))

	got := testutil.ToFloat64(m.requests.WithLabelValues(http.MethodPost, "/known", "202"))
	if got != 1 {
		t.Fatalf("known route count = %v, want 1", got)
	}
	got = testutil.ToFloat64(m.requests.WithLabelValues(http.MethodGet, "/magnets/{infohash}", "202"))
	if got != 1 {
		t.Fatalf("magnet route count = %v, want 1", got)
	}
	got = testutil.ToFloat64(m.requests.WithLabelValues(http.MethodGet, "other", "202"))
	if got != 1 {
		t.Fatalf("other route count = %v, want 1", got)
	}

	if _, err := reg.Gather(); err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
}

func TestHTTPWrapSkipsMCPStreamDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newHTTP(reg, "testapp", map[string]string{"/mcp": "/mcp"})
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp", http.NoBody))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", http.NoBody))

	if got := testutil.ToFloat64(m.requests.WithLabelValues(http.MethodGet, "/mcp", "202")); got != 1 {
		t.Fatalf("GET /mcp request count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues(http.MethodPost, "/mcp", "202")); got != 1 {
		t.Fatalf("POST /mcp request count = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(m.duration); got != 1 {
		t.Fatalf("HTTP duration series count = %d, want only POST /mcp", got)
	}
	assertHistogramCount(t, m.duration.WithLabelValues(http.MethodPost, "/mcp"), 1)
}

func TestConstructorsUseIndependentRegistries(t *testing.T) {
	q := fakeQueueStats{}
	_ = NewTakuhai("v", "c", q)
	_ = NewTakuhai("v", "c", q)
	_ = NewDMHY("v", "c")
	_ = NewDMHY("v", "c")
}

func TestSubmitConfidenceRecordsMatchedAndSuppressed(t *testing.T) {
	m := NewTakuhai("v", "c", fakeQueueStats{})

	matched := 0.94
	suppressed := 0.73
	unmatched := 0.51
	m.Submit("matched", "ok", &matched)
	m.Submit("suppressed", "ok", &suppressed)
	m.Submit("unmatched", "ok", &unmatched)
	m.Submit("matched", "error", &matched)

	assertHistogram(t, m.submitConfidence.WithLabelValues("matched"), 1, matched)
	assertHistogram(t, m.submitConfidence.WithLabelValues("suppressed"), 1, suppressed)
}

func TestQueueStatsErrorDoesNotFailScrape(t *testing.T) {
	m := NewTakuhai("v", "c", fakeQueueStats{err: errors.New("boom")})
	rec := httptest.NewRecorder()

	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	if !strings.Contains(string(body), "takuhai_queue_stats_scrape_ok 0") {
		t.Fatalf("/metrics missing queue scrape failure gauge:\n%s", body)
	}
}

func TestTakuhaiPrecreatesDMHYIngestPostCounters(t *testing.T) {
	m := NewTakuhai("v", "c", fakeQueueStats{})
	rec := httptest.NewRecorder()

	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	text := string(body)
	for _, result := range []string{"new", "updated", "duplicate", "conflict", "skipped", "error"} {
		want := `takuhai_ingest_posts_total{result="` + result + `",source="dmhy"} 0`
		if !strings.Contains(text, want) {
			t.Fatalf("/metrics missing %q:\n%s", want, text)
		}
	}
}

func TestCatalogStatsScrape(t *testing.T) {
	m := NewTakuhai("v", "c", fakeQueueStats{
		catalog: store.CatalogStats{RawPosts: 7, Infohashes: 3, Refs: 2},
	})
	rec := httptest.NewRecorder()

	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"takuhai_catalog_raw_posts 7",
		"takuhai_catalog_infohashes 3",
		"takuhai_catalog_refs 2",
		"takuhai_catalog_stats_scrape_ok 1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("/metrics missing %q:\n%s", want, text)
		}
	}
}

type fakeQueueStats struct {
	err     error
	catalog store.CatalogStats
}

func (f fakeQueueStats) QueueStats(context.Context) (store.QueueStats, error) {
	return store.QueueStats{Available: 1}, f.err
}

func (f fakeQueueStats) CatalogStats(context.Context) (store.CatalogStats, error) {
	return f.catalog, f.err
}

func assertHistogram(t *testing.T, metric prometheus.Observer, count uint64, sum float64) {
	t.Helper()
	var pb dto.Metric
	if err := metric.(prometheus.Metric).Write(&pb); err != nil {
		t.Fatalf("write histogram: %v", err)
	}
	h := pb.GetHistogram()
	if h.GetSampleCount() != count || h.GetSampleSum() != sum {
		t.Fatalf("histogram = count %d sum %v, want count %d sum %v", h.GetSampleCount(), h.GetSampleSum(), count, sum)
	}
}

func assertHistogramCount(t *testing.T, metric prometheus.Observer, count uint64) {
	t.Helper()
	var pb dto.Metric
	if err := metric.(prometheus.Metric).Write(&pb); err != nil {
		t.Fatalf("write histogram: %v", err)
	}
	if pb.GetHistogram().GetSampleCount() != count {
		t.Fatalf("histogram count = %d, want %d", pb.GetHistogram().GetSampleCount(), count)
	}
}
