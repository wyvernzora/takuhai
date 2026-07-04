package crawl

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wyvernzora/takuhai/pkg/rawpost"
)

// PageFetcher fetches the raw bytes for a 1-based page number. It is the network seam
// the crawl page-walk reads pages through, factored out so tests can drive
// deterministic offline fixtures and inject transient fetch failures WITHOUT touching
// live sources.
//
// Contract: ANY fetch failure (network, non-2xx, parse) MUST return a non-nil error -
// a transient failure must never look like an end-of-archive empty page. A successful
// 200 OK returns the page bytes and a nil error, even when the page parses to zero
// content rows (the empty-but-paginated floor page the consecutive-empty terminator
// detects).
type PageFetcher func(ctx context.Context, page int) (body []byte, err error)

// PageParser parses one fetched page into newest-to-oldest raw posts.
type PageParser func(body []byte) ([]rawpost.RawPost, error)

// Metrics records crawler metrics. internal/metrics.Crawler satisfies it, but the
// interface keeps this package source-agnostic and nil-friendly.
type Metrics interface {
	Crawl(result string, posts int, dur time.Duration)
	Fetch(result string, dur time.Duration)
	ParsePosts(result string, posts int)
}

// CrawlRequest is the POST /crawl request body. n8n owns page size and the lookback
// window; the cursor is opaque.
//
//   - page_size: posts to return per call, clamped to 1-200.
//   - cursor: opaque resume point; "" starts at the latest (page 1).
//   - lookback: extended Go duration (12h, 30d, 2w); the crawler drops posts older
//     than now - lookback. "" or 0 = no limit.
type CrawlRequest struct {
	PageSize int    `json:"page_size"`
	Cursor   string `json:"cursor"`
	Lookback string `json:"lookback"`
}

// CrawlResponse is the POST /crawl response body. posts are the in-window raw posts,
// newest -> oldest; next_cursor is the opaque cursor to thread into the next /crawl
// ("" when has_more=false); has_more is false once the lookback boundary OR the archive
// floor is reached.
type CrawlResponse struct {
	Posts      []rawpost.RawPost `json:"posts"`
	NextCursor string            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`

	StopReason   string `json:"-"`
	PagesFetched int    `json:"-"`
	LastPage     int    `json:"-"`
}

// Config wires a stateless source crawler.
type Config struct {
	Source            string
	Fetch             PageFetcher
	Parse             PageParser
	Threshold         int
	FloorReason       string
	ParseErrorContext func(page int) string
	Now               func() time.Time
	Metrics           Metrics
}

// Crawler is the stateless source crawl engine behind POST /crawl. It owns the
// within-request page-walk and the consecutive-empty archive-floor threshold; it holds
// NO cursor state across requests (n8n persists next_cursor). The threshold N is the
// consecutive_empty_threshold from the source's floor fixture. now is the injectable
// clock the lookback cutoff reads (never time.Now() inline).
type Crawler struct {
	source            string
	fetch             PageFetcher
	parse             PageParser
	threshold         int
	floorReason       string
	parseErrorContext func(page int) string
	now               func() time.Time
	metrics           Metrics
}

// NewCrawler constructs a stateless crawler over a page fetcher and parser.
func NewCrawler(cfg Config) *Crawler {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Crawler{
		source:            cfg.Source,
		fetch:             cfg.Fetch,
		parse:             cfg.Parse,
		threshold:         cfg.Threshold,
		floorReason:       cfg.FloorReason,
		parseErrorContext: cfg.ParseErrorContext,
		now:               now,
		metrics:           cfg.Metrics,
	}
}

var (
	// ErrCrawlFetch marks an upstream page fetch failure.
	ErrCrawlFetch = errors.New("crawl fetch")
	// ErrCrawlParse marks a fetched page that could not be parsed.
	ErrCrawlParse = errors.New("crawl parse")
)

// Crawl runs one /crawl request. It walks source pages from the cursor, returning up
// to pageSize in-window posts (newest -> oldest), per the §1 algorithm:
//
//   - Cursor decodes to (page, offset): the page last consumed and the count of leading
//     rows on it already returned. "" = (0, 0) so the first fetch targets page 1.
//   - lookback cutoff: drop any post with published_at < now - lookback. The walk is
//     newest -> oldest, so the first out-of-window post means the rest are too -> stop,
//     has_more=false. Posts with a zero/unparseable published_at are KEPT (a parse
//     glitch must not truncate the window).
//   - The consecutive-empty counter is in-process per walk (NOT persisted): a single
//     Crawl MUST resolve any empty run it enters before returning - keep walking
//     through empties until content appears OR the threshold trips (has_more=false,
//     next_cursor=""). It NEVER parks a next_cursor inside an unresolved empty run.
//   - has_more is true only when the budget filled AND the next unreturned post is
//     still in-window; false at the lookback boundary OR the archive floor (next_cursor
//     is "" in both).
//   - ANY fetch/parse failure surfaces as a non-nil error and NEVER looks like the
//     floor (zero CrawlResponse, cursor not advanced past the failed page). lookback is
//     parsed by the caller (ServeHTTP) and passed pre-resolved, so the engine is free
//     of string parsing.
func (c *Crawler) Crawl(ctx context.Context, req CrawlRequest, lookback time.Duration) (CrawlResponse, error) {
	curPage, curOffset, err := ParseCursor(c.source, req.Cursor)
	if err != nil {
		return CrawlResponse{}, err
	}

	pageSize := clampPageSize(req.PageSize)

	// cutoff is the lookback boundary; a zero lookback means "no limit" (no row is ever
	// out-of-window). Computed once off the injectable clock so the test seam is exact.
	var cutoff time.Time
	hasCutoff := lookback > 0
	if hasCutoff {
		cutoff = c.now().Add(-lookback)
	}

	var (
		posts            []rawpost.RawPost
		consecutiveEmpty int
		// page is the page to fetch next; skip is the leading rows to drop on the FIRST
		// fetched page. An offset>0 cursor resumes ON its page (rows remain); an offset==0
		// cursor means that page is fully consumed -> start at page+1.
		page         = curPage
		skip         = curOffset
		pagesFetched int
		lastPage     int
	)
	if skip == 0 {
		page++
	}

	for {
		body, err := c.fetch(ctx, page)
		pagesFetched++
		lastPage = page
		if err != nil {
			// A transient fetch failure surfaces verbatim and must NOT look like the floor
			// nor advance past the failed page (design §1/§5/§8): a retry re-fetches the
			// SAME page, leaving no permanent gap.
			return CrawlResponse{}, fmt.Errorf("%s: %w: %w", c.source, ErrCrawlFetch, err)
		}
		pagePosts, err := c.parsePage(body, page)
		if err != nil {
			// A parse failure is a fetch failure under the §8 contract - surface it, never
			// treat unparseable bytes as an empty page.
			return CrawlResponse{}, err
		}
		recordParsePosts(c.metrics, "ok", len(pagePosts))

		if len(pagePosts) == 0 {
			// Positively-confirmed empty page (200 OK, zero parsed rows). Extend the empty
			// run; once it reaches the threshold the archive floor is positively confirmed.
			// We must resolve the empty run (content or floor) before returning, never park
			// a next_cursor inside it.
			consecutiveEmpty++
			if consecutiveEmpty >= c.threshold {
				return response(posts, "", false, c.floorReason, pagesFetched, lastPage), nil
			}
			page++
			skip = 0
			continue
		}

		// A non-empty page resets the empty run.
		consecutiveEmpty = 0

		// On the first fetched page of THIS call, drop the leading rows the cursor already
		// returned. pageStart is the index of the first row this page considers.
		pageStart := skip
		skip = 0
		if pageStart > len(pagePosts) {
			pageStart = len(pagePosts)
		}

		for i := pageStart; i < len(pagePosts); i++ {
			p := pagePosts[i]
			if outOfWindow(p.PublishedAt, cutoff, hasCutoff) {
				// Newest -> oldest: the first out-of-window post means all following are too.
				return response(posts, "", false, "lookback_boundary", pagesFetched, lastPage), nil
			}
			posts = append(posts, p)

			if len(posts) < pageSize {
				continue
			}

			// Budget filled. Resolve has_more against rows ALREADY IN HAND before parking a
			// cursor - never blindly emit has_more=true.
			if i+1 < len(pagePosts) {
				// More rows remain on THIS page. We have their bytes, so apply the same
				// predicate to the next one (free, and mandatory: has_more=false <=> lookback
				// boundary).
				if outOfWindow(pagePosts[i+1].PublishedAt, cutoff, hasCutoff) {
					// The boundary lands exactly at the budget edge; the caller is done.
					return response(posts, "", false, "lookback_boundary", pagesFetched, lastPage), nil
				}
				// Resume mid-page at offset (i+1): the count of this page's rows now returned.
				return response(posts, FormatCursor(page, i+1), true, "page_budget", pagesFetched, lastPage), nil
			}
			// Budget filled exactly at the page's last row. The next row lives on an
			// unfetched page+1 - do NOT peek it. Park (page, 0): decode treats offset==0 as
			// "this page fully consumed -> page++ before fetching", so it resumes by fetching
			// page+1. (Parking (page+1, 0) would decode to page+2 and silently drop every row
			// of page+1.)
			return response(posts, FormatCursor(page, 0), true, "page_budget", pagesFetched, lastPage), nil
		}

		// Page exhausted without filling the budget - advance to the next page.
		page++
	}
}

func (c *Crawler) parsePage(body []byte, page int) ([]rawpost.RawPost, error) {
	pagePosts, err := c.parse(body)
	if err == nil {
		return pagePosts, nil
	}
	contextText := ""
	if c.parseErrorContext != nil {
		contextText = c.parseErrorContext(page)
	}
	if contextText != "" {
		return nil, fmt.Errorf("%s: %w: %s: %w", c.source, ErrCrawlParse, contextText, err)
	}
	return nil, fmt.Errorf("%s: %w: page %d: %w", c.source, ErrCrawlParse, page, err)
}

func response(posts []rawpost.RawPost, nextCursor string, hasMore bool, stopReason string, pagesFetched, lastPage int) CrawlResponse {
	return CrawlResponse{
		Posts:        posts,
		NextCursor:   nextCursor,
		HasMore:      hasMore,
		StopReason:   stopReason,
		PagesFetched: pagesFetched,
		LastPage:     lastPage,
	}
}

// outOfWindow reports whether a post's published_at falls before the lookback cutoff.
// A zero/unparseable timestamp (year 0001) is KEPT in-window: a parse glitch must not
// truncate the walk. When there is no cutoff (lookback <= 0) nothing is out-of-window.
func outOfWindow(publishedAt, cutoff time.Time, hasCutoff bool) bool {
	if !hasCutoff {
		return false
	}
	return !publishedAt.IsZero() && publishedAt.Before(cutoff)
}

func clampPageSize(n int) int {
	if n < 1 {
		return 1
	}
	if n > 200 {
		return 200
	}
	return n
}

// ParseCursor decodes the opaque (page, offset) resume point: the page last consumed
// and the count of leading rows on it already returned. "" means nothing has been
// fetched yet ((0, 0), so the next fetch targets page 1). A malformed cursor is a hard
// error rather than a silent restart.
func ParseCursor(source, cursor string) (page, offset int, err error) {
	if cursor == "" {
		return 0, 0, nil
	}
	p, o, ok := strings.Cut(cursor, ":")
	if !ok {
		return 0, 0, fmt.Errorf("%s: malformed cursor %q", source, cursor)
	}
	page, err = strconv.Atoi(p)
	if err != nil || page < 0 {
		return 0, 0, fmt.Errorf("%s: malformed cursor %q", source, cursor)
	}
	offset, err = strconv.Atoi(o)
	if err != nil || offset < 0 {
		return 0, 0, fmt.Errorf("%s: malformed cursor %q", source, cursor)
	}
	return page, offset, nil
}

// FormatCursor encodes the (page, offset) resume point into the opaque cursor string
// the caller threads into the next Crawl.
func FormatCursor(page, offset int) string {
	return strconv.Itoa(page) + ":" + strconv.Itoa(offset)
}
