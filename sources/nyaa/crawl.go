package nyaa

import (
	"context"
	"time"

	"github.com/wyvernzora/takuhai/internal/metrics"
	"github.com/wyvernzora/takuhai/pkg/crawl"
	"github.com/wyvernzora/takuhai/pkg/rawpost"
)

// PageFetcher fetches the raw HTML bytes for a 1-based Nyaa result page.
type PageFetcher func(ctx context.Context, page int) (body []byte, err error)

// CrawlRequest is the POST /crawl request body.
type CrawlRequest = crawl.CrawlRequest

// CrawlResponse is the POST /crawl response body.
type CrawlResponse struct {
	Posts      []rawpost.RawPost `json:"posts"`
	NextCursor string            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`

	stopReason   string `json:"-"`
	pagesFetched int    `json:"-"`
	lastPage     int    `json:"-"`
}

// Crawler is the stateless Nyaa crawl engine behind POST /crawl.
type Crawler struct {
	fetch     PageFetcher
	threshold int
	now       func() time.Time
	metrics   *metrics.Crawler
}

// NewCrawler constructs a stateless crawler over a page fetcher.
func NewCrawler(fetch PageFetcher, threshold int) *Crawler {
	return &Crawler{fetch: fetch, threshold: threshold, now: time.Now}
}

func (c *Crawler) Crawl(ctx context.Context, req CrawlRequest, lookback time.Duration) (CrawlResponse, error) {
	resp, err := c.shared().Crawl(ctx, req, lookback)
	if err != nil {
		return CrawlResponse{}, err
	}
	return crawlResponse(resp), nil
}

func (c *Crawler) shared() *crawl.Crawler {
	return crawl.NewCrawler(crawl.Config{
		Source:      "nyaa",
		Fetch:       crawl.PageFetcher(c.fetch),
		Parse:       ParseListingPage,
		Threshold:   c.threshold,
		FloorReason: "feed_floor",
		Now:         c.now,
		Metrics:     c.metrics,
	})
}

func crawlResponse(resp crawl.CrawlResponse) CrawlResponse {
	return CrawlResponse{
		Posts:        resp.Posts,
		NextCursor:   resp.NextCursor,
		HasMore:      resp.HasMore,
		stopReason:   resp.StopReason,
		pagesFetched: resp.PagesFetched,
		lastPage:     resp.LastPage,
	}
}

func parseCursor(cursor string) (page, offset int, err error) {
	return crawl.ParseCursor("nyaa", cursor)
}

func formatCursor(page, offset int) string {
	return crawl.FormatCursor(page, offset)
}
