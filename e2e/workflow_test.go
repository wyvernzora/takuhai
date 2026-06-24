//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/wyvernzora/takuhai/pkg/rawpost"
)

const (
	matchInfohash    = "0123456789abcdef0123456789abcdef01234567"
	suppressInfohash = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	exhaustInfohash  = "1111111111111111111111111111111111111111"
	unknownInfohash  = "ffffffffffffffffffffffffffffffffffffffff"
)

func TestEndToEndWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	nw, err := tcnetwork.New(ctx)
	if err != nil {
		t.Fatalf("create docker network: %v", err)
	}
	t.Cleanup(func() {
		removeCtx, removeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer removeCancel()
		if err := nw.Remove(removeCtx); err != nil {
			t.Logf("cleanup network: %v", err)
		}
	})

	pg := startPostgres(t, ctx, nw)
	startFakeDMHY(t, ctx, nw)
	crawlerURL := startCrawler(t, ctx, nw)
	takuhaiURL := startTakuhai(t, ctx, nw, pg)

	posts := crawlDMHY(t, crawlerURL)
	ingestPosts(t, takuhaiURL, posts)

	matchToken := claimOne(t, takuhaiURL, matchInfohash, 30)
	assertSubmitStatus(t, takuhaiURL, http.StatusConflict, map[string]any{
		"infohash":    matchInfohash,
		"claim_token": matchToken - 1,
		"status":      "matched",
		"ref":         "tvdb:12345",
		"confidence":  0.5,
	})
	assertSubmitStatus(t, takuhaiURL, http.StatusBadRequest, map[string]any{
		"infohash":    matchInfohash,
		"claim_token": matchToken,
		"status":      "defer",
	})
	submitOK(t, takuhaiURL, map[string]any{
		"infohash":    matchInfohash,
		"claim_token": matchToken,
		"status":      "matched",
		"ref":         "tvdb:12345",
		"confidence":  0,
		"reason":      "e2e exact fixture match",
	})

	suppressToken := claimOne(t, takuhaiURL, suppressInfohash, 30)
	submitOK(t, takuhaiURL, map[string]any{
		"infohash":    suppressInfohash,
		"claim_token": suppressToken,
		"status":      "suppressed",
		"reason":      "e2e not wanted",
	})

	exhaustToken := claimOne(t, takuhaiURL, exhaustInfohash, 1)
	submitOK(t, takuhaiURL, map[string]any{
		"infohash":    exhaustInfohash,
		"claim_token": exhaustToken,
		"status":      "unmatched",
		"reason":      "e2e first miss",
	})
	assertNoClaim(t, takuhaiURL, "unmatched submit keeps the lease until timeout")
	time.Sleep(1500 * time.Millisecond)
	exhaustToken = claimOne(t, takuhaiURL, exhaustInfohash, 1)
	submitOK(t, takuhaiURL, map[string]any{
		"infohash":    exhaustInfohash,
		"claim_token": exhaustToken,
		"status":      "unmatched",
		"reason":      "e2e second miss exhausts",
	})

	stats := queueStats(t, takuhaiURL)
	if stats.Matched != 1 || stats.Suppressed != 1 || stats.Exhausted != 1 || stats.Available != 0 || stats.Leased != 0 {
		t.Fatalf("queue stats = %+v, want matched=1 suppressed=1 exhausted=1 available=0 leased=0", stats)
	}

	assertMCP(t, ctx, takuhaiURL, posts[0].Magnet)
}

func startPostgres(t *testing.T, ctx context.Context, nw *testcontainers.DockerNetwork) *tcpostgres.PostgresContainer {
	t.Helper()
	pg, err := tcpostgres.Run(ctx,
		"postgres:17-alpine",
		tcpostgres.WithDatabase("takuhai"),
		tcpostgres.WithUsername("takuhai"),
		tcpostgres.WithPassword("takuhai"),
		tcnetwork.WithNetwork([]string{"postgres"}, nw),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = pg.Terminate(stopCtx)
	})
	return pg
}

func startFakeDMHY(t *testing.T, ctx context.Context, nw *testcontainers.DockerNetwork) testcontainers.Container {
	t.Helper()
	dir := fakeDMHYContext(t)
	c, err := testcontainers.Run(ctx, "",
		testcontainers.WithDockerfile(testcontainers.FromDockerfile{
			Context:        dir,
			Dockerfile:     "Dockerfile",
			Repo:           "takuhai-e2e-dmhy",
			Tag:            "latest",
			BuildLogWriter: os.Stderr,
		}),
		tcnetwork.WithNetwork([]string{"dmhy"}, nw),
		testcontainers.WithExposedPorts("80/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/topics/list/sort_id/2/page/1").WithPort("80/tcp").WithStartupTimeout(2*time.Minute)),
	)
	if err != nil {
		t.Fatalf("start fake dmhy container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = c.Terminate(stopCtx)
	})
	return c
}

func startCrawler(t *testing.T, ctx context.Context, nw *testcontainers.DockerNetwork) string {
	t.Helper()
	c, err := testcontainers.Run(ctx, "",
		testcontainers.WithDockerfile(testcontainers.FromDockerfile{
			Context:        repoRoot(t),
			Dockerfile:     "Dockerfile",
			Repo:           "takuhai-e2e-crawler",
			Tag:            "latest",
			BuildArgs:      buildArgs(map[string]string{"BUILD_DIR": "sources/dmhy", "CMD_PKG": "./cmd/takuhai-dmhy", "VERSION": "e2e"}),
			BuildLogWriter: os.Stderr,
		}),
		tcnetwork.WithNetwork([]string{"crawler"}, nw),
		testcontainers.WithEnv(map[string]string{
			"TAKUHAI_DMHY_ADDR":      ":8080",
			"TAKUHAI_DMHY_BASE_URL":  "http://dmhy",
			"TAKUHAI_DMHY_SORT_ID":   "2",
			"TAKUHAI_DMHY_RATE_RPS":  "0",
			"TAKUHAI_DMHY_CACHE_TTL": "0",
			"TAKUHAI_DMHY_LOG_LEVEL": "debug",
		}),
		testcontainers.WithExposedPorts("8080/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("8080/tcp").WithStartupTimeout(2*time.Minute)),
	)
	if err != nil {
		t.Fatalf("start crawler container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = c.Terminate(stopCtx)
	})
	endpoint, err := c.Endpoint(ctx, "http")
	if err != nil {
		t.Fatalf("crawler endpoint: %v", err)
	}
	return endpoint
}

func startTakuhai(t *testing.T, ctx context.Context, nw *testcontainers.DockerNetwork, pg *tcpostgres.PostgresContainer) string {
	t.Helper()
	c, err := testcontainers.Run(ctx, "",
		testcontainers.WithDockerfile(testcontainers.FromDockerfile{
			Context:        repoRoot(t),
			Dockerfile:     "Dockerfile",
			Repo:           "takuhai-e2e-service",
			Tag:            "latest",
			BuildArgs:      buildArgs(map[string]string{"BUILD_DIR": ".", "CMD_PKG": "./cmd/takuhai", "VERSION": "e2e"}),
			BuildLogWriter: os.Stderr,
		}),
		tcnetwork.WithNetwork([]string{"takuhai"}, nw),
		testcontainers.WithEnv(map[string]string{
			"TAKUHAI_ADDR":               ":8080",
			"TAKUHAI_DATABASE_URL":       "postgres://takuhai:takuhai@postgres:5432/takuhai?sslmode=disable",
			"TAKUHAI_QUEUE_MAX_ATTEMPTS": "2",
			"TAKUHAI_LOG_LEVEL":          "debug",
		}),
		testcontainers.WithExposedPorts("8080/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/healthz").WithPort("8080/tcp").WithStartupTimeout(2*time.Minute)),
	)
	if err != nil {
		t.Fatalf("start takuhai container after postgres %s: %v", pg.MustConnectionString(ctx, "sslmode=disable"), err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		_ = c.Terminate(stopCtx)
	})
	endpoint, err := c.Endpoint(ctx, "http")
	if err != nil {
		t.Fatalf("takuhai endpoint: %v", err)
	}
	return endpoint
}

func crawlDMHY(t *testing.T, crawlerURL string) []rawpost.RawPost {
	t.Helper()
	var first crawlResponse
	postJSON(t, crawlerURL+"/crawl", map[string]any{
		"page_size": 2,
		"lookback":  "365d",
	}, http.StatusOK, &first)
	if len(first.Posts) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first crawl = %+v, want two posts with has_more", first)
	}
	if first.Posts[0].Title != "E2E Match Release" || !strings.Contains(first.Posts[0].Magnet, "tr=udp://tracker.match/announce") {
		t.Fatalf("first crawled post = %+v, want tracker-rich match release", first.Posts[0])
	}
	if first.Posts[0].SizeBytes != 1500000000 {
		t.Fatalf("first post size = %d, want 1500000000", first.Posts[0].SizeBytes)
	}

	var second crawlResponse
	postJSON(t, crawlerURL+"/crawl", map[string]any{
		"page_size": 2,
		"cursor":    first.NextCursor,
		"lookback":  "365d",
	}, http.StatusOK, &second)
	if len(second.Posts) != 1 || second.HasMore || second.NextCursor != "" {
		t.Fatalf("second crawl = %+v, want final single post", second)
	}
	return append(first.Posts, second.Posts...)
}

func ingestPosts(t *testing.T, takuhaiURL string, posts []rawpost.RawPost) {
	t.Helper()
	var summary rawpost.IngestSummary
	postJSON(t, takuhaiURL+"/ingest", map[string]any{"posts": posts}, http.StatusOK, &summary)
	if summary.Batch.New != 3 || summary.Batch.Skipped != 0 || summary.Queue.Available != 3 {
		t.Fatalf("ingest summary = %+v, want three new available posts", summary)
	}
}

func claimOne(t *testing.T, takuhaiURL, wantInfohash string, leaseSeconds int) int64 {
	t.Helper()
	var claim claimResponse
	postJSON(t, takuhaiURL+"/queue/claim", map[string]any{
		"limit":         1,
		"lease_seconds": leaseSeconds,
	}, http.StatusOK, &claim)
	if len(claim.Items) != 1 {
		t.Fatalf("claim items = %+v, want one item %s", claim.Items, wantInfohash)
	}
	item := claim.Items[0]
	if item.Infohash != wantInfohash {
		t.Fatalf("claimed infohash = %s, want %s", item.Infohash, wantInfohash)
	}
	if item.ClaimToken == 0 || item.AttemptCount == 0 {
		t.Fatalf("claimed item = %+v, want token and attempt", item)
	}
	if len(item.RawItems) != 1 || item.RawItems[0].Source != rawpost.SourceDMHY || item.RawItems[0].Title == "" {
		t.Fatalf("claimed raw items = %+v, want crawled DMHY evidence", item.RawItems)
	}
	return item.ClaimToken
}

func assertNoClaim(t *testing.T, takuhaiURL, note string) {
	t.Helper()
	var claim claimResponse
	postJSON(t, takuhaiURL+"/queue/claim", map[string]any{
		"limit":         1,
		"lease_seconds": 1,
	}, http.StatusOK, &claim)
	if len(claim.Items) != 0 {
		t.Fatalf("claim during %s = %+v, want no items", note, claim.Items)
	}
}

func submitOK(t *testing.T, takuhaiURL string, body map[string]any) {
	t.Helper()
	assertSubmitStatus(t, takuhaiURL, http.StatusOK, body)
}

func assertSubmitStatus(t *testing.T, takuhaiURL string, wantStatus int, body map[string]any) {
	t.Helper()
	var out map[string]any
	postJSON(t, takuhaiURL+"/submit", body, wantStatus, &out)
}

func queueStats(t *testing.T, takuhaiURL string) queueStatsResponse {
	t.Helper()
	resp, err := http.Get(takuhaiURL + "/queue/stats")
	if err != nil {
		t.Fatalf("GET /queue/stats: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /queue/stats = %d, want 200", resp.StatusCode)
	}
	var stats queueStatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode /queue/stats: %v", err)
	}
	return stats
}

func assertMCP(t *testing.T, ctx context.Context, takuhaiURL, wantMagnet string) {
	t.Helper()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "takuhai-e2e", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: takuhaiURL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("MCP connect: %v", err)
	}
	defer session.Close()

	list := callTool[listReleasesResponse](t, ctx, session, "list_releases", map[string]any{"ref": "tvdb:12345", "limit": 10})
	if len(list.Releases) != 1 {
		t.Fatalf("list_releases returned %d releases, want 1", len(list.Releases))
	}
	release := list.Releases[0]
	if release.Infohash != matchInfohash || release.Confidence != 0 {
		t.Fatalf("list_releases item = %+v, want matched infohash with zero confidence", release)
	}
	if _, ok := release.Raw["magnet"]; ok {
		t.Fatalf("list_releases leaked magnet field: %+v", release.Raw)
	}
	if _, ok := release.Raw["ref"]; ok {
		t.Fatalf("list_releases leaked ref field: %+v", release.Raw)
	}
	empty := callTool[listReleasesResponse](t, ctx, session, "list_releases", map[string]any{"ref": "tvdb:99999", "limit": 10})
	if len(empty.Releases) != 0 {
		t.Fatalf("list_releases for empty ref = %+v, want none", empty.Releases)
	}

	resolved := callTool[resolveMagnetsResponse](t, ctx, session, "resolve_magnets", map[string]any{
		"infohashes": []string{matchInfohash, unknownInfohash},
	})
	if got := resolved.Magnets[matchInfohash]; got != wantMagnet {
		t.Fatalf("resolved magnet = %q, want %q", got, wantMagnet)
	}
	if _, ok := resolved.Magnets[unknownInfohash]; ok {
		t.Fatalf("resolve_magnets returned unknown infohash: %+v", resolved.Magnets)
	}
}

func callTool[T any](t *testing.T, ctx context.Context, session *mcpsdk.ClientSession, name string, args map[string]any) T {
	t.Helper()
	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("MCP %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("MCP %s returned IsError: %s", name, firstText(res))
	}
	var out T
	if err := json.Unmarshal([]byte(firstText(res)), &out); err != nil {
		t.Fatalf("decode MCP %s response %q: %v", name, firstText(res), err)
	}
	return out
}

func postJSON(t *testing.T, url string, body any, wantStatus int, out any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s: %v", url, err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		var raw bytes.Buffer
		_, _ = raw.ReadFrom(resp.Body)
		t.Fatalf("POST %s = %d, want %d; body=%s", url, resp.StatusCode, wantStatus, raw.String())
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
}

func firstText(res *mcpsdk.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func buildArgs(in map[string]string) map[string]*string {
	out := make(map[string]*string, len(in))
	for k, v := range in {
		v := v
		out[k] = &v
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, ".."))
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		t.Fatalf("resolve repo root from %s: %v", wd, err)
	}
	return root
}

func fakeDMHYContext(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Dockerfile"), `FROM nginx:1.29-alpine
COPY topics /usr/share/nginx/html/topics
`)
	writeFile(t, filepath.Join(dir, "topics/list/sort_id/2/page/1"), fakeArchivePage(
		row("1001", "E2E Match Release", matchInfohash, "1.5GB", "2026/06/24 22:25", "udp://tracker.match/announce"),
		row("1002", "E2E Suppress Release", suppressInfohash, "700MB", "2026/06/24 22:24", "udp://tracker.suppress/announce"),
		row("1003", "E2E Exhaust Release", exhaustInfohash, "350MB", "2026/06/24 22:23", "udp://tracker.exhaust/announce"),
	))
	empty := fakeArchivePage()
	writeFile(t, filepath.Join(dir, "topics/list/sort_id/2/page/2"), empty)
	writeFile(t, filepath.Join(dir, "topics/list/sort_id/2/page/3"), empty)
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func fakeArchivePage(rows ...string) string {
	return `<!doctype html><html><body><table id="topic_list"><tbody>` + strings.Join(rows, "\n") + `</tbody></table></body></html>`
}

func row(sourceID, title, infohash, size, published, tracker string) string {
	magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s&tr=%s&tr=http://tracker.common/announce", infohash, tracker)
	bare := fmt.Sprintf("magnet:?xt=urn:btih:%s", infohash)
	return fmt.Sprintf(`<tr class="">
<td class="title"><a href="/topics/view/%s_e2e.html">%s</a></td>
<td><a class="arrow-magnet" href="%s">download</a><a data-magnet="%s"></a></td>
<td align="center">%s</td>
<td><span style="display: none;">%s</span></td>
</tr>`, sourceID, title, magnet, bare, size, published)
}

type crawlResponse struct {
	Posts      []rawpost.RawPost `json:"posts"`
	NextCursor string            `json:"next_cursor"`
	HasMore    bool              `json:"has_more"`
}

type claimResponse struct {
	Items []claimItem `json:"items"`
}

type claimItem struct {
	Infohash     string    `json:"infohash"`
	ClaimToken   int64     `json:"claim_token"`
	AttemptCount int       `json:"attempt_count"`
	RawItems     []rawItem `json:"raw_items"`
}

type rawItem struct {
	Source string `json:"source"`
	Title  string `json:"title"`
}

type queueStatsResponse struct {
	Available  int `json:"available"`
	Leased     int `json:"leased"`
	Unmatched  int `json:"unmatched"`
	Matched    int `json:"matched"`
	Suppressed int `json:"suppressed"`
	Exhausted  int `json:"exhausted"`
}

type listReleasesResponse struct {
	Releases []releaseItem `json:"releases"`
}

type releaseItem struct {
	Infohash   string         `json:"infohash"`
	Confidence float64        `json:"confidence"`
	Raw        map[string]any `json:"-"`
}

func (r *releaseItem) UnmarshalJSON(b []byte) error {
	type alias releaseItem
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*r = releaseItem(a)
	r.Raw = raw
	return nil
}

type resolveMagnetsResponse struct {
	Magnets map[string]string `json:"magnets"`
}
