package crawl

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
)

// Server is the stateless POST /crawl HTTP handler. It holds no cursor state; n8n
// persists next_cursor. It wraps a Crawler over a source-specific PageFetcher,
// optionally rate-limited by the caller's fetcher.
type Server struct {
	source  string
	crawler *Crawler
	metrics Metrics
	logger  *slog.Logger
}

// ServerConfig wires the shared POST /crawl HTTP shell.
type ServerConfig struct {
	Source  string
	Crawler *Crawler
	Metrics Metrics
	Logger  *slog.Logger
}

// NewServer constructs a shared POST /crawl handler.
func NewServer(cfg ServerConfig) *Server {
	return &Server{
		source:  cfg.Source,
		crawler: cfg.Crawler,
		metrics: cfg.Metrics,
		logger:  cfg.Logger,
	}
}

func (s *Server) log(r *http.Request, level slog.Level, msg string, attrs ...any) {
	if s.logger == nil {
		return
	}
	s.logger.Log(r.Context(), level, msg, attrs...)
}

// ServeHTTP handles POST /crawl: decode the request, validate the client-side params
// (lookback, cursor), run the page-walk, and encode the {posts, next_cursor, has_more}
// response. A malformed body / lookback / cursor is a client error (400); a crawl
// (fetch/parse) failure is 502 - a transient upstream failure, never the archive floor.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodPost {
		recordCrawl(s.metrics, "bad_request", 0, time.Since(start))
		s.log(r, slog.LevelWarn, "crawl rejected", "reason", "method_not_allowed", "method", r.Method)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req CrawlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		recordCrawl(s.metrics, "bad_request", 0, time.Since(start))
		s.log(r, slog.LevelWarn, "crawl rejected", "reason", "invalid_body", "err", err)
		http.Error(w, fmt.Sprintf("%s: decode /crawl request: %v", s.source, err), http.StatusBadRequest)
		return
	}
	// Validate the client-side params BEFORE the walk: a malformed lookback or cursor is
	// a bad request param (400), not an upstream fetch failure (502). The engine takes
	// the pre-resolved lookback and never string-parses.
	lookback, err := ParseLookback(s.source, req.Lookback)
	if err != nil {
		recordCrawl(s.metrics, "bad_request", 0, time.Since(start))
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
	if _, _, err := ParseCursor(s.source, req.Cursor); err != nil {
		recordCrawl(s.metrics, "bad_request", 0, time.Since(start))
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
		if errors.Is(err, ErrCrawlParse) {
			result = "parse_error"
		}
		recordCrawl(s.metrics, result, 0, time.Since(start))
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
		recordCrawl(s.metrics, "error", len(resp.Posts), time.Since(start))
		s.log(r, slog.LevelError, "crawl response encode failed",
			"post_count", len(resp.Posts),
			"duration_ms", time.Since(start).Milliseconds(),
			"err", err,
		)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	recordCrawl(s.metrics, "ok", len(resp.Posts), time.Since(start))
	s.log(r, slog.LevelInfo, "crawl completed",
		"page_size", req.PageSize,
		"post_count", len(resp.Posts),
		"has_more", resp.HasMore,
		"has_next_cursor", resp.NextCursor != "",
		"cursor", req.Cursor,
		"next_cursor", resp.NextCursor,
		"lookback", req.Lookback,
		"stop_reason", resp.StopReason,
		"pages_fetched", resp.PagesFetched,
		"last_page", resp.LastPage,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// HTTPFetcher fetches source pages over HTTP or file:// fixtures.
type HTTPFetcher struct {
	source      string
	buildURL    func(page int) (string, error)
	limiter     *rate.Limiter
	client      *http.Client
	metrics     Metrics
	readFileURL func(path string) ([]byte, error)
}

// HTTPFetcherConfig wires HTTP/file fetching for one source.
type HTTPFetcherConfig struct {
	Source     string
	BuildURL   func(page int) (string, error)
	RatePerSec float64
	Client     *http.Client
	Metrics    Metrics
}

// NewHTTPFetcher constructs a PageFetcher over HTTP and file:// URLs.
func NewHTTPFetcher(cfg HTTPFetcherConfig) *HTTPFetcher {
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	f := &HTTPFetcher{
		source:   cfg.Source,
		buildURL: cfg.BuildURL,
		client:   client,
		metrics:  cfg.Metrics,
		readFileURL: func(path string) ([]byte, error) {
			return ReadFileURL(cfg.Source, path)
		},
	}
	if cfg.RatePerSec > 0 {
		f.limiter = rate.NewLimiter(rate.Limit(cfg.RatePerSec), 1)
	}
	return f
}

// FetchPage fetches one 1-based page.
func (f *HTTPFetcher) FetchPage(ctx context.Context, page int) ([]byte, error) {
	if f.limiter != nil {
		if err := f.limiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("%s: rate limiter: %w", f.source, err)
		}
	}

	start := time.Now()
	result := "error"
	defer func() { recordFetch(f.metrics, result, time.Since(start)) }()

	target, err := f.buildURL(page)
	if err != nil {
		return nil, err
	}

	if rest, ok := strings.CutPrefix(target, "file://"); ok {
		b, err := f.readFileURL(rest)
		if err == nil {
			result = "ok"
		}
		return b, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("%s: build request %s: %w", f.source, target, err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: fetch %s: %w", f.source, target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: fetch %s: status %d", f.source, target, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read body %s: %w", f.source, target, err)
	}
	result = "ok"
	return b, nil
}

// ReadFileURL reads a file:// page body for the offline path. A trailing query string
// is stripped before the filesystem read.
func ReadFileURL(source, path string) ([]byte, error) {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: read file %s: %w", source, path, err)
	}
	return b, nil
}

func recordCrawl(m Metrics, result string, posts int, dur time.Duration) {
	if m != nil {
		m.Crawl(result, posts, dur)
	}
}

func recordFetch(m Metrics, result string, dur time.Duration) {
	if m != nil {
		m.Fetch(result, dur)
	}
}

func recordParsePosts(m Metrics, result string, posts int) {
	if m != nil {
		m.ParsePosts(result, posts)
	}
}
