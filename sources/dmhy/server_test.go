package dmhy

import (
	"context"
	"strings"
	"testing"
)

// TestArchivePageURLSortID pins the archive URL builder the --sort-id flag feeds: a
// positive sort_id yields the filtered path, 0 yields the bare path.
func TestArchivePageURLSortID(t *testing.T) {
	const base = "https://share.dmhy.org"
	cases := []struct {
		sortID, page int
		want         string
	}{
		{2, 5, "https://share.dmhy.org/topics/list/sort_id/2/page/5"},
		{31, 1, "https://share.dmhy.org/topics/list/sort_id/31/page/1"},
		{0, 5, "https://share.dmhy.org/topics/list/page/5"},
	}
	for _, c := range cases {
		if got := archivePageURL(base, c.sortID, c.page); got != c.want {
			t.Fatalf("archivePageURL(%q, %d, %d) = %q, want %q", base, c.sortID, c.page, got, c.want)
		}
	}
}

// TestNewServerSortIDReachesCrawl proves the --sort-id flag threaded through NewServer
// reaches the live Crawl path's internal c.sortID read (crawl.go: c.fetch(ctx,
// c.sortID, page)). It drives the REAL Crawl over an unreadable file:// base so the
// fetch fails AFTER building the target URL — the returned error embeds the URL, which
// must carry sort_id/31. A re-hardcode of c.fetch(ctx, 0, page) inside Crawl would make
// the error carry the bare path instead, which this assertion catches; passing sortID
// in as a parameter could not.
func TestNewServerSortIDReachesCrawl(t *testing.T) {
	s := NewServer("file:///nonexistent/takuhai-dmhy-test", 31, 0, 0)

	_, err := s.crawler.Crawl(context.Background(), CrawlRequest{PageSize: 1}, 0)
	if err == nil {
		t.Fatal("Crawl: want fetch error over missing file:// base, got nil")
	}
	if !strings.Contains(err.Error(), "sort_id/31") {
		t.Fatalf("Crawl error %q does not carry sort_id/31 — the configured sortID did not reach c.fetch", err.Error())
	}
}
