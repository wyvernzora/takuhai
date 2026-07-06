package metrics

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/wyvernzora/takuhai/internal/store"
	"github.com/wyvernzora/takuhai/pkg/rawpost"
)

type HTTP struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	routes   map[string]string
}

func newHTTP(reg prometheus.Registerer, namespace string, routes map[string]string) *HTTP {
	return &HTTP{
		requests: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total HTTP requests.",
		}, []string{"method", "path", "status"}),
		duration: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "path"}),
		routes: routes,
	}
}

func (m *HTTP) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		captured := httpsnoop.CaptureMetrics(next, w, r)
		path := m.route(r.URL.Path)
		status := strconv.Itoa(captured.Code)
		m.requests.WithLabelValues(r.Method, path, status).Inc()
		if r.Method != http.MethodGet || path != "/mcp" {
			m.duration.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
		}
	})
}

func (m *HTTP) route(path string) string {
	if route, ok := m.routes[path]; ok {
		return route
	}
	for prefix, route := range m.routes {
		if strings.HasSuffix(prefix, "/") && strings.HasPrefix(path, prefix) {
			return route
		}
	}
	return "other"
}

func (m *HTTP) Route(path string) string { return m.route(path) }

type Takuhai struct {
	HTTP                 *HTTP
	handler              http.Handler
	ingestBatches        *prometheus.CounterVec
	ingestPosts          *prometheus.CounterVec
	ingestBatchSize      prometheus.Histogram
	queueClaims          *prometheus.CounterVec
	queueClaimedItems    prometheus.Counter
	queueClaimBatchSize  prometheus.Histogram
	submits              *prometheus.CounterVec
	submitConfidence     *prometheus.HistogramVec
	mcpToolCalls         *prometheus.CounterVec
	mcpToolDuration      *prometheus.HistogramVec
	mcpResolveInfohashes *prometheus.CounterVec
}

func NewTakuhai(version, commit string, qs queueStatsProvider) *Takuhai {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registerBuildInfo(reg, "takuhai", version, commit)
	reg.MustRegister(&queueCollector{source: qs})
	auto := promauto.With(reg)
	m := &Takuhai{
		handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		HTTP: newHTTP(reg, "takuhai", map[string]string{
			"/healthz":     "/healthz",
			"/ingest":      "/ingest",
			"/magnets/":    "/magnets/{infohash}",
			"/releases/":   "/releases/{infohash}",
			"/mcp":         "/mcp",
			"/metrics":     "/metrics",
			"/queue/claim": "/queue/claim",
			"/queue/stats": "/queue/stats",
			"/submit":      "/submit",
		}),
		ingestBatches: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "takuhai",
			Subsystem: "ingest",
			Name:      "batches_total",
			Help:      "Total ingest batches.",
		}, []string{"result"}),
		ingestPosts: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "takuhai",
			Subsystem: "ingest",
			Name:      "posts_total",
			Help:      "Total ingest posts by source and result.",
		}, []string{"source", "result"}),
		ingestBatchSize: auto.NewHistogram(prometheus.HistogramOpts{
			Namespace: "takuhai",
			Subsystem: "ingest",
			Name:      "batch_size",
			Help:      "Posts per ingest batch.",
			Buckets:   []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
		}),
		queueClaims: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "takuhai",
			Subsystem: "queue",
			Name:      "claims_total",
			Help:      "Total queue claim requests.",
		}, []string{"result"}),
		queueClaimedItems: auto.NewCounter(prometheus.CounterOpts{
			Namespace: "takuhai",
			Subsystem: "queue",
			Name:      "claimed_items_total",
			Help:      "Total queue items claimed.",
		}),
		queueClaimBatchSize: auto.NewHistogram(prometheus.HistogramOpts{
			Namespace: "takuhai",
			Subsystem: "queue",
			Name:      "claim_batch_size",
			Help:      "Items per non-empty queue claim.",
			Buckets:   []float64{1, 2, 5, 10, 25, 50, 100},
		}),
		submits: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "takuhai",
			Subsystem: "submit",
			Name:      "total",
			Help:      "Total matcher submissions.",
		}, []string{"status", "result"}),
		submitConfidence: auto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "takuhai",
			Subsystem: "submit",
			Name:      "confidence",
			Help:      "Successful matcher submission confidence.",
			Buckets:   []float64{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 0.99, 1},
		}, []string{"status"}),
		mcpToolCalls: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "takuhai",
			Subsystem: "mcp",
			Name:      "tool_calls_total",
			Help:      "Total MCP tool calls.",
		}, []string{"tool", "result"}),
		mcpToolDuration: auto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "takuhai",
			Subsystem: "mcp",
			Name:      "tool_duration_seconds",
			Help:      "MCP tool call duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"tool"}),
		mcpResolveInfohashes: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "takuhai",
			Subsystem: "mcp",
			Name:      "resolve_magnets_infohashes_total",
			Help:      "Total resolve_magnets infohash lookups.",
		}, []string{"result"}),
	}
	for _, source := range rawpost.Sources() {
		for _, result := range []string{"new", "updated", "duplicate", "conflict", "skipped", "error"} {
			m.ingestPosts.WithLabelValues(source, result).Add(0)
		}
	}
	return m
}

func (m *Takuhai) Handler() http.Handler { return m.handler }

func (m *Takuhai) IngestBatch(size int, result string) {
	if m == nil {
		return
	}
	m.ingestBatches.WithLabelValues(result).Inc()
	m.ingestBatchSize.Observe(float64(size))
}

func (m *Takuhai) IngestPost(source, result string) {
	if m == nil {
		return
	}
	if source == "" {
		source = "unknown"
	}
	m.ingestPosts.WithLabelValues(source, result).Inc()
}

func (m *Takuhai) QueueClaim(count int, result string) {
	if m == nil {
		return
	}
	m.queueClaims.WithLabelValues(result).Inc()
	if count > 0 {
		m.queueClaimedItems.Add(float64(count))
		m.queueClaimBatchSize.Observe(float64(count))
	}
}

func (m *Takuhai) Submit(status, result string, confidence *float64) {
	if m == nil {
		return
	}
	status = submitStatus(status)
	m.submits.WithLabelValues(status, result).Inc()
	if result == "ok" && confidence != nil && (status == "matched" || status == "suppressed") {
		m.submitConfidence.WithLabelValues(status).Observe(*confidence)
	}
}

func (m *Takuhai) MCPTool(tool, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.mcpToolCalls.WithLabelValues(tool, result).Inc()
	m.mcpToolDuration.WithLabelValues(tool).Observe(dur.Seconds())
}

func (m *Takuhai) MCPResolveMagnets(hits, misses int) {
	if m == nil {
		return
	}
	if hits > 0 {
		m.mcpResolveInfohashes.WithLabelValues("hit").Add(float64(hits))
	}
	if misses > 0 {
		m.mcpResolveInfohashes.WithLabelValues("miss").Add(float64(misses))
	}
}

func submitStatus(status string) string {
	switch status {
	case "matched", "unmatched", "suppressed":
		return status
	default:
		return "invalid"
	}
}

type queueStatsProvider interface {
	QueueStats(ctx context.Context) (store.QueueStats, error)
	CatalogStats(ctx context.Context) (store.CatalogStats, error)
}

type queueCollector struct {
	source queueStatsProvider
}

var queueItemsDesc = prometheus.NewDesc(
	"takuhai_queue_items",
	"Current queue items by state.",
	[]string{"state"},
	nil,
)

var queueStatsScrapeOKDesc = prometheus.NewDesc(
	"takuhai_queue_stats_scrape_ok",
	"Whether queue stats were available during the metrics scrape.",
	nil,
	nil,
)

var catalogRawPostsDesc = prometheus.NewDesc(
	"takuhai_catalog_raw_posts",
	"Current number of raw release posts.",
	nil,
	nil,
)

var catalogInfohashesDesc = prometheus.NewDesc(
	"takuhai_catalog_infohashes",
	"Current number of unique release infohashes.",
	nil,
	nil,
)

var catalogRefsDesc = prometheus.NewDesc(
	"takuhai_catalog_refs",
	"Current number of unique non-empty refs.",
	nil,
	nil,
)

var catalogStatsScrapeOKDesc = prometheus.NewDesc(
	"takuhai_catalog_stats_scrape_ok",
	"Whether catalog stats were available during the metrics scrape.",
	nil,
	nil,
)

func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- queueItemsDesc
	ch <- queueStatsScrapeOKDesc
	ch <- catalogRawPostsDesc
	ch <- catalogInfohashesDesc
	ch <- catalogRefsDesc
	ch <- catalogStatsScrapeOKDesc
}

func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cs, err := c.source.CatalogStats(ctx)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(catalogStatsScrapeOKDesc, prometheus.GaugeValue, 0)
	} else {
		ch <- prometheus.MustNewConstMetric(catalogStatsScrapeOKDesc, prometheus.GaugeValue, 1)
		ch <- prometheus.MustNewConstMetric(catalogRawPostsDesc, prometheus.GaugeValue, float64(cs.RawPosts))
		ch <- prometheus.MustNewConstMetric(catalogInfohashesDesc, prometheus.GaugeValue, float64(cs.Infohashes))
		ch <- prometheus.MustNewConstMetric(catalogRefsDesc, prometheus.GaugeValue, float64(cs.Refs))
	}

	qs, err := c.source.QueueStats(ctx)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(queueStatsScrapeOKDesc, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(queueStatsScrapeOKDesc, prometheus.GaugeValue, 1)
	for state, value := range map[string]int{
		"claimable":  qs.Available,
		"leased":     qs.Leased,
		"unmatched":  qs.Unmatched,
		"matched":    qs.Matched,
		"suppressed": qs.Suppressed,
		"exhausted":  qs.Exhausted,
	} {
		ch <- prometheus.MustNewConstMetric(queueItemsDesc, prometheus.GaugeValue, float64(value), state)
	}
}

// Crawler records Prometheus metrics for one stateless crawler service.
type Crawler struct {
	HTTP             *HTTP
	handler          http.Handler
	crawlRequests    *prometheus.CounterVec
	crawlDuration    prometheus.Histogram
	crawlPages       prometheus.Counter
	crawlPosts       prometheus.Counter
	crawlPostsPerReq prometheus.Histogram
	fetchRequests    *prometheus.CounterVec
	fetchDuration    prometheus.Histogram
	parsePosts       *prometheus.CounterVec
}

// NewCrawler constructs the metric surface used by one stateless crawler.
func NewCrawler(namespace, sourceName, version, commit string) *Crawler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registerBuildInfo(reg, namespace, version, commit)
	auto := promauto.With(reg)
	return &Crawler{
		handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		HTTP: newHTTP(reg, namespace, map[string]string{
			"/crawl":   "/crawl",
			"/metrics": "/metrics",
		}),
		crawlRequests: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "crawl",
			Name:      "requests_total",
			Help:      "Total crawl requests.",
		}, []string{"result"}),
		crawlDuration: auto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "crawl",
			Name:      "duration_seconds",
			Help:      "Crawl request duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}),
		crawlPages: auto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "crawl",
			Name:      "pages_fetched_total",
			Help:      "Total " + sourceName + " pages fetched successfully.",
		}),
		crawlPosts: auto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "crawl",
			Name:      "posts_returned_total",
			Help:      "Total posts returned from crawl requests.",
		}),
		crawlPostsPerReq: auto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "crawl",
			Name:      "posts_per_request",
			Help:      "Posts returned per crawl request.",
			Buckets:   []float64{0, 1, 5, 10, 25, 50, 100, 200},
		}),
		fetchRequests: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "fetch",
			Name:      "requests_total",
			Help:      "Total upstream " + sourceName + " page fetches.",
		}, []string{"result"}),
		fetchDuration: auto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "fetch",
			Name:      "duration_seconds",
			Help:      "Upstream " + sourceName + " page fetch duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}),
		parsePosts: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "parse",
			Name:      "posts_total",
			Help:      "Total posts parsed from " + sourceName + " pages.",
		}, []string{"result"}),
	}
}

func (m *Crawler) Handler() http.Handler { return m.handler }

func (m *Crawler) Crawl(result string, posts int, dur time.Duration) {
	if m == nil {
		return
	}
	m.crawlRequests.WithLabelValues(result).Inc()
	m.crawlDuration.Observe(dur.Seconds())
	m.crawlPostsPerReq.Observe(float64(posts))
	if posts > 0 {
		m.crawlPosts.Add(float64(posts))
	}
}

func (m *Crawler) Fetch(result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.fetchRequests.WithLabelValues(result).Inc()
	m.fetchDuration.Observe(dur.Seconds())
	if result == "ok" {
		m.crawlPages.Inc()
	}
}

func (m *Crawler) ParsePosts(result string, posts int) {
	if m == nil {
		return
	}
	if posts > 0 {
		m.parsePosts.WithLabelValues(result).Add(float64(posts))
	}
}

func registerBuildInfo(reg prometheus.Registerer, namespace, version, commit string) {
	promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Build metadata.",
	}, []string{"version", "commit"}).WithLabelValues(version, commit).Set(1)
}
