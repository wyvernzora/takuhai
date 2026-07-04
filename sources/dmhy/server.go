package dmhy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wyvernzora/takuhai/internal/metrics"
	"github.com/wyvernzora/takuhai/pkg/crawl"
)

// Threshold is the DMHY consecutive-empty end-of-archive threshold N. It is the
// consecutive_empty_threshold recorded in floor.json (the single source of truth the
// fixtures are built to). It is small and stable, so it is pinned here as the
// crawler's runtime default rather than read from a file at boot.
const Threshold = 2

// archivePageURL builds a DMHY HTML archive URL from a base URL. The base URL is the
// scheme+host the crawler scrapes (e.g. https://share.dmhy.org); a file:// base reads
// local files for the offline path. The archive walk is
// {base}/topics/list/sort_id/{sort_id}/page/{page}.
func archivePageURL(base string, sortID, page int) string {
	b := strings.TrimRight(base, "/")
	if sortID > 0 {
		return fmt.Sprintf("%s/topics/list/sort_id/%d/page/%d", b, sortID, page)
	}
	return fmt.Sprintf("%s/topics/list/page/%d", b, page)
}

// Server is the stateless POST /crawl HTTP handler.
type Server struct {
	crawler *Crawler
	fetcher *crawl.HTTPFetcher
	handler *crawl.Server
}

func NewServerWithMetricsAndLogger(baseURL string, sortID int, ratePerSec float64, cacheTTL time.Duration, m *metrics.Crawler, logger *slog.Logger) *Server {
	s := &Server{}
	fetcher := crawl.NewHTTPFetcher(crawl.HTTPFetcherConfig{
		Source: "dmhy",
		BuildURL: func(page int) (string, error) {
			currentSortID := sortID
			if s.crawler != nil {
				currentSortID = s.crawler.sortID
			}
			return archivePageURL(baseURL, currentSortID, page), nil
		},
		RatePerSec: ratePerSec,
		Metrics:    m,
	})
	fetch := PageFetcher(func(ctx context.Context, _ int, page int) ([]byte, error) {
		return fetcher.FetchPage(ctx, page)
	})
	if cacheTTL > 0 {
		fetch = newPageCache(cacheTTL).wrap(fetch)
	}
	s.crawler = NewCrawler(fetch, Threshold)
	s.crawler.sortID = sortID
	s.crawler.metrics = m
	s.fetcher = fetcher
	s.handler = crawl.NewServer(crawl.ServerConfig{
		Source:  "dmhy",
		Crawler: s.crawler.shared(),
		Metrics: m,
		Logger:  logger,
	})
	return s
}

// newServerWithFetcher constructs a Server whose crawl runs over an injected
// PageFetcher and threshold, bypassing the live HTTP/file fetch. It exists so the
// conformance suite can exercise the REAL POST /crawl HTTP boundary (method routing,
// status mapping, JSON round-trip) over deterministic offline fixtures.
func newServerWithFetcher(fetch PageFetcher, threshold int) *Server {
	c := NewCrawler(fetch, threshold)
	return &Server{
		crawler: c,
		handler: crawl.NewServer(crawl.ServerConfig{
			Source:  "dmhy",
			Crawler: c.shared(),
		}),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// fetchPage is kept for source tests that assert rate limiter behavior through the
// concrete server.
func (s *Server) fetchPage(ctx context.Context, sortID, page int) ([]byte, error) {
	return s.fetcher.FetchPage(ctx, page)
}

func (c *Crawler) shared() *crawl.Crawler {
	return crawl.NewCrawler(crawl.Config{
		Source:      "dmhy",
		Fetch:       func(ctx context.Context, page int) ([]byte, error) { return c.fetch(ctx, c.sortID, page) },
		Parse:       ParseArchivePage,
		Threshold:   c.threshold,
		FloorReason: "archive_floor",
		ParseErrorContext: func(page int) string {
			return fmt.Sprintf("sort_id %d page %d", c.sortID, page)
		},
		Now:     c.now,
		Metrics: c.metrics,
	})
}

func readFileURL(path string) ([]byte, error) {
	return crawl.ReadFileURL("dmhy", path)
}
