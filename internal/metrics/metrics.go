package metrics

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/wyvernzora/takuhai/internal/store"
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
		m.duration.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
	})
}

func (m *HTTP) route(path string) string {
	if route, ok := m.routes[path]; ok {
		return route
	}
	return "other"
}

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
	return &Takuhai{
		handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		HTTP: newHTTP(reg, "takuhai", map[string]string{
			"/healthz":     "/healthz",
			"/ingest":      "/ingest",
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

func (m *Takuhai) Submit(status, result string) {
	if m == nil {
		return
	}
	m.submits.WithLabelValues(submitStatus(status), result).Inc()
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

type DMHY struct {
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

func NewDMHY(version, commit string) *DMHY {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registerBuildInfo(reg, "takuhai_dmhy", version, commit)
	auto := promauto.With(reg)
	return &DMHY{
		handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		HTTP: newHTTP(reg, "takuhai_dmhy", map[string]string{
			"/crawl":   "/crawl",
			"/metrics": "/metrics",
		}),
		crawlRequests: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "takuhai_dmhy",
			Subsystem: "crawl",
			Name:      "requests_total",
			Help:      "Total crawl requests.",
		}, []string{"result"}),
		crawlDuration: auto.NewHistogram(prometheus.HistogramOpts{
			Namespace: "takuhai_dmhy",
			Subsystem: "crawl",
			Name:      "duration_seconds",
			Help:      "Crawl request duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}),
		crawlPages: auto.NewCounter(prometheus.CounterOpts{
			Namespace: "takuhai_dmhy",
			Subsystem: "crawl",
			Name:      "pages_fetched_total",
			Help:      "Total DMHY archive pages fetched successfully.",
		}),
		crawlPosts: auto.NewCounter(prometheus.CounterOpts{
			Namespace: "takuhai_dmhy",
			Subsystem: "crawl",
			Name:      "posts_returned_total",
			Help:      "Total posts returned from crawl requests.",
		}),
		crawlPostsPerReq: auto.NewHistogram(prometheus.HistogramOpts{
			Namespace: "takuhai_dmhy",
			Subsystem: "crawl",
			Name:      "posts_per_request",
			Help:      "Posts returned per crawl request.",
			Buckets:   []float64{0, 1, 5, 10, 25, 50, 100, 200},
		}),
		fetchRequests: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "takuhai_dmhy",
			Subsystem: "fetch",
			Name:      "requests_total",
			Help:      "Total upstream DMHY page fetches.",
		}, []string{"result"}),
		fetchDuration: auto.NewHistogram(prometheus.HistogramOpts{
			Namespace: "takuhai_dmhy",
			Subsystem: "fetch",
			Name:      "duration_seconds",
			Help:      "Upstream DMHY page fetch duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}),
		parsePosts: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "takuhai_dmhy",
			Subsystem: "parse",
			Name:      "posts_total",
			Help:      "Total posts parsed from DMHY archive pages.",
		}, []string{"result"}),
	}
}

func (m *DMHY) Handler() http.Handler { return m.handler }

func (m *DMHY) Crawl(result string, posts int, dur time.Duration) {
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

func (m *DMHY) Fetch(result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.fetchRequests.WithLabelValues(result).Inc()
	m.fetchDuration.Observe(dur.Seconds())
	if result == "ok" {
		m.crawlPages.Inc()
	}
}

func (m *DMHY) ParsePosts(result string, posts int) {
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
