package nyaa

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/wyvernzora/takuhai/internal/metrics"
	"github.com/wyvernzora/takuhai/pkg/crawl"
)

// Threshold is the Nyaa consecutive-empty feed-floor threshold.
const Threshold = 2

// Server is the stateless POST /crawl HTTP handler.
type Server struct {
	crawler *Crawler
	fetcher *crawl.HTTPFetcher
	handler *crawl.Server
}

// NewServerWithMetricsAndLogger constructs a crawl server with metrics and structured logs.
func NewServerWithMetricsAndLogger(baseURL, query, category, filter string, ratePerSec float64, m *metrics.Crawler, logger *slog.Logger) *Server {
	fetcher := crawl.NewHTTPFetcher(crawl.HTTPFetcherConfig{
		Source: "nyaa",
		BuildURL: func(page int) (string, error) {
			return listingPageURL(baseURL, query, category, filter, page)
		},
		RatePerSec: ratePerSec,
		Metrics:    m,
	})
	c := NewCrawler(fetcher.FetchPage, Threshold)
	c.metrics = m
	return &Server{
		crawler: c,
		fetcher: fetcher,
		handler: crawl.NewServer(crawl.ServerConfig{
			Source:  "nyaa",
			Crawler: c.shared(),
			Metrics: m,
			Logger:  logger,
		}),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func listingPageURL(base, search, category, filter string, page int) (string, error) {
	if strings.HasPrefix(base, "file://") {
		return fmt.Sprintf("%s/page-%d.html", strings.TrimRight(base, "/"), page), nil
	}
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", fmt.Errorf("nyaa: parse base url %q: %w", base, err)
	}
	if u.Path == "" {
		u.Path = "/"
	}
	q := u.Query()
	q.Set("p", strconv.Itoa(page))
	if search != "" {
		q.Set("q", search)
	}
	if category != "" {
		q.Set("c", category)
	}
	if filter != "" {
		q.Set("f", filter)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (s *Server) fetchPage(ctx context.Context, page int) ([]byte, error) {
	return s.fetcher.FetchPage(ctx, page)
}

func readFileURL(path string) ([]byte, error) {
	return crawl.ReadFileURL("nyaa", path)
}
