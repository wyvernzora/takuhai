package dmhy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/wyvernzora/takuhai/internal/metrics"
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

// Server is the stateless POST /crawl HTTP handler. It holds no cursor state; n8n
// persists next_cursor. It wraps a Crawler over an HTTP/file PageFetcher, optionally
// rate-limited so the crawler stays a polite citizen against live DMHY.
type Server struct {
	baseURL string
	crawler *Crawler
	limiter *rate.Limiter
	client  *http.Client
	metrics *metrics.DMHY
	logger  *slog.Logger
}

// NewServer constructs the crawl server over a DMHY base URL and a request rate
// (requests per second; <= 0 disables rate limiting). sortID is the DMHY sort_id the
// archive walk targets (a per-deployment knob; <=0 uses the bare archive path).
// cacheTTL is the in-memory page cache lifetime: within it a given (sort_id, page) is
// fetched from DMHY at most once (<= 0 disables the cache). The threshold is the pinned
// Threshold const.
func NewServer(baseURL string, sortID int, ratePerSec float64, cacheTTL time.Duration) *Server {
	return NewServerWithMetrics(baseURL, sortID, ratePerSec, cacheTTL, nil)
}

func NewServerWithMetrics(baseURL string, sortID int, ratePerSec float64, cacheTTL time.Duration, m *metrics.DMHY) *Server {
	return NewServerWithMetricsAndLogger(baseURL, sortID, ratePerSec, cacheTTL, m, nil)
}

func NewServerWithMetricsAndLogger(baseURL string, sortID int, ratePerSec float64, cacheTTL time.Duration, m *metrics.DMHY, logger *slog.Logger) *Server {
	s := &Server{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
		metrics: m,
		logger:  logger,
	}
	if ratePerSec > 0 {
		s.limiter = rate.NewLimiter(rate.Limit(ratePerSec), 1)
	}
	fetch := PageFetcher(s.fetchPage)
	if cacheTTL > 0 {
		fetch = newPageCache(cacheTTL).wrap(fetch)
	}
	s.crawler = NewCrawler(fetch, Threshold)
	s.crawler.sortID = sortID
	s.crawler.metrics = m
	return s
}

// newServerWithFetcher constructs a Server whose crawl runs over an injected
// PageFetcher and threshold, bypassing the live HTTP/file fetch. It exists so the
// conformance suite can exercise the REAL POST /crawl HTTP boundary (method routing,
// status mapping, JSON round-trip) over deterministic offline fixtures.
func newServerWithFetcher(fetch PageFetcher, threshold int) *Server {
	return &Server{crawler: NewCrawler(fetch, threshold)}
}

func (s *Server) log(r *http.Request, level slog.Level, msg string, attrs ...any) {
	if s.logger == nil {
		return
	}
	s.logger.Log(r.Context(), level, msg, attrs...)
}

// fetchPage is the live PageFetcher: it builds the DMHY archive URL for the given
// page and reads the bytes (HTTP or file://), honoring the rate limiter. ANY failure
// (build, network, non-2xx, read) surfaces as a non-nil error so a transient blip
// never looks like an empty page (the §1/§5 contract).
func (s *Server) fetchPage(ctx context.Context, sortID, page int) ([]byte, error) {
	if s.limiter != nil {
		if err := s.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("dmhy: rate limiter: %w", err)
		}
	}

	start := time.Now()
	result := "error"
	defer func() { s.metrics.Fetch(result, time.Since(start)) }()

	target := archivePageURL(s.baseURL, sortID, page)

	if rest, ok := strings.CutPrefix(target, "file://"); ok {
		b, err := readFileURL(rest)
		if err == nil {
			result = "ok"
		}
		return b, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("dmhy: build request %s: %w", target, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dmhy: fetch %s: %w", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dmhy: fetch %s: status %d", target, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("dmhy: read body %s: %w", target, err)
	}
	result = "ok"
	return b, nil
}

// ServeHTTP handles POST /crawl: decode the request, validate the client-side params
// (lookback, cursor), run the page-walk, and encode the {posts, next_cursor, has_more}
// response. A malformed body / lookback / cursor is a client error (400); a crawl
// (fetch/parse) failure is 502 — a transient upstream failure, never the archive floor.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodPost {
		s.metrics.Crawl("bad_request", 0, time.Since(start))
		s.log(r, slog.LevelWarn, "crawl rejected", "reason", "method_not_allowed", "method", r.Method)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req CrawlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.metrics.Crawl("bad_request", 0, time.Since(start))
		s.log(r, slog.LevelWarn, "crawl rejected", "reason", "invalid_body", "err", err)
		http.Error(w, fmt.Sprintf("dmhy: decode /crawl request: %v", err), http.StatusBadRequest)
		return
	}
	// Validate the client-side params BEFORE the walk: a malformed lookback or cursor is
	// a bad request param (400), not an upstream fetch failure (502). The engine takes
	// the pre-resolved lookback and never string-parses.
	lookback, err := parseLookback(req.Lookback)
	if err != nil {
		s.metrics.Crawl("bad_request", 0, time.Since(start))
		s.log(r, slog.LevelWarn, "crawl rejected",
			"reason", "invalid_lookback",
			"page_size", req.PageSize,
			"cursor", req.Cursor,
			"lookback", req.Lookback,
			"err", err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, _, err := parseCursor(req.Cursor); err != nil {
		s.metrics.Crawl("bad_request", 0, time.Since(start))
		s.log(r, slog.LevelWarn, "crawl rejected",
			"reason", "invalid_cursor",
			"page_size", req.PageSize,
			"cursor_len", len(req.Cursor),
			"lookback", req.Lookback,
			"err", err,
		)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := s.crawler.Crawl(r.Context(), req, lookback)
	if err != nil {
		result := "fetch_error"
		if errors.Is(err, errCrawlParse) {
			result = "parse_error"
		}
		s.metrics.Crawl(result, 0, time.Since(start))
		s.log(r, slog.LevelWarn, "crawl failed",
			"result", result,
			"page_size", req.PageSize,
			"cursor", req.Cursor,
			"lookback", req.Lookback,
			"duration_ms", time.Since(start).Milliseconds(),
			"err", err,
		)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.metrics.Crawl("error", len(resp.Posts), time.Since(start))
		s.log(r, slog.LevelError, "crawl response encode failed",
			"post_count", len(resp.Posts),
			"duration_ms", time.Since(start).Milliseconds(),
			"err", err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.metrics.Crawl("ok", len(resp.Posts), time.Since(start))
	s.log(r, slog.LevelInfo, "crawl completed",
		"page_size", req.PageSize,
		"post_count", len(resp.Posts),
		"has_more", resp.HasMore,
		"has_next_cursor", resp.NextCursor != "",
		"cursor", req.Cursor,
		"next_cursor", resp.NextCursor,
		"lookback", req.Lookback,
		"stop_reason", resp.stopReason,
		"pages_fetched", resp.pagesFetched,
		"last_page", resp.lastPage,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// readFileURL reads a file:// page body for the offline path. A trailing query string
// is stripped before the filesystem read (the HTML archive URL carries none, so this
// is a harmless no-op for the live path).
func readFileURL(path string) ([]byte, error) {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("dmhy: read file %s: %w", path, err)
	}
	return b, nil
}
