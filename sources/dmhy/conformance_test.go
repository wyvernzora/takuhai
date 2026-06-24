//go:build conformance

// sources/dmhy crawler conformance suite. These tests are the in-module successors of
// the root-suite dmhy tests (TestP0 manifest, TestP4 backfill, the ParseSize size
// golden), reshaped for the DUMB crawler + the stateless POST /crawl surface and the
// §1 contract (page_size / lookback / opaque (page,offset) cursor / has_more):
//
//   - The HTML parser emits EVERY parsed row as a rawpost.RawPost with NO infohash —
//     including pure-v2/malformed rows the OLD parser skipped. takuhai derives the
//     dedup key and the skipped bucket on /ingest.
//   - The page-walk + consecutive-empty threshold is the stateless Crawler behind POST
//     /crawl: empty-run continuity (a single /crawl resolves any empty run before
//     returning), the page_size budget bounds content, the lookback window bounds time,
//     and a transient fetch error never looks like the archive floor.
//
// One file, in-package (package dmhy) mirroring the root suite's in-package manifest
// gate, so fixtures load from this module's own testdata/ with no ../ crossing into
// the root module. The clock is injected via c.now (package-internal) so lookback /
// has_more are deterministic.

package dmhy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wyvernzora/takuhai/pkg/rawpost"
)

// ---------------------------------------------------------------------------
// Shared fixture roots + loaders (testdata/ is THIS module's own copy — no ../).
// ---------------------------------------------------------------------------

const (
	htmlDir  = "testdata/html"
	floorPth = "testdata/floor.json"
)

func loadHTMLBytes(t *testing.T, rel string) []byte {
	t.Helper()
	p := filepath.Join(htmlDir, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%s: cannot read HTML fixture: %v", p, err)
	}
	return b
}

// floorArtifact mirrors floor.json's load-bearing fields. The threshold N is read
// here (NOT hard-coded) so the fixtures, this suite, and the crawler all consume the
// same committed value.
type floorArtifact struct {
	Sources struct {
		Dmhy struct {
			SortID31 struct {
				FloorPage     *int   `json:"floor_page"`
				CalendarFloor string `json:"calendar_floor"`
			} `json:"sort_id_31"`
		} `json:"dmhy"`
	} `json:"sources"`
	CrawlRateRPS              float64 `json:"crawl_rate_rps"`
	ConsecutiveEmptyThreshold int     `json:"consecutive_empty_threshold"`
}

func loadFloor(t *testing.T) floorArtifact {
	t.Helper()
	b, err := os.ReadFile(floorPth)
	if err != nil {
		t.Fatalf("%s: cannot read floor artifact: %v", floorPth, err)
	}
	var fa floorArtifact
	if err := json.Unmarshal(b, &fa); err != nil {
		t.Fatalf("%s: floor artifact is not valid JSON: %v", floorPth, err)
	}
	return fa
}

func threshold(t *testing.T) int {
	t.Helper()
	n := loadFloor(t).ConsecutiveEmptyThreshold
	if n <= 0 {
		t.Fatalf("%s: consecutive_empty_threshold = %d, want a positive int N", floorPth, n)
	}
	return n
}

var contentRowRe = regexp.MustCompile(`<tr class="">`)

func pageRows(b []byte) int { return len(contentRowRe.FindAllString(string(b), -1)) }

// seqPages loads page-1..page-N of a sequence dir, asserting exactly N pages exist.
func seqPages(t *testing.T, seqDir string, n int) [][]byte {
	t.Helper()
	dir := filepath.Join(htmlDir, seqDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read sequence dir %s: %v", dir, err)
	}
	htmlCount := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".html" {
			htmlCount++
		}
	}
	if htmlCount != n {
		t.Fatalf("%s: sequence has %d page-*.html files, want exactly N=%d", dir, htmlCount, n)
	}
	pages := make([][]byte, n)
	for i := 0; i < n; i++ {
		pages[i] = loadHTMLBytes(t, filepath.Join(seqDir, "page-"+strconv.Itoa(i+1)+".html"))
	}
	return pages
}

// seqFetcher builds a deterministic PageFetcher over a fixed page list: page i
// (1-based) returns pages[i-1]; any page beyond the list repeats the LAST page's
// bytes (so an all-empty seq stays empty past the list and the threshold trips; a
// guard seq ending non-empty stays non-empty so a crawl never terminates).
func seqFetcher(pages [][]byte) PageFetcher {
	return func(ctx context.Context, sortID, page int) ([]byte, error) {
		if page < 1 {
			page = 1
		}
		if page > len(pages) {
			page = len(pages)
		}
		return pages[page-1], nil
	}
}

// ---------------------------------------------------------------------------
// Synthetic page builder + scripted fetcher (F0-9, F0-10).
//
// The committed fixtures top out at 3 content rows and seqFetcher only repeats its last
// page, so neither the 200-row clamp ceiling NOR a mid-page (page, offset>0) resume is
// observable with them. These helpers emit pages with an arbitrary row count, each row
// carrying a distinct recoverable SourceID/title/magnet and a settable hidden date, and
// a fetcher that serves a SCRIPTED list of distinct page bodies by page number.
// ---------------------------------------------------------------------------

// rowSpec is one synthetic archive row. id makes the SourceID/title/magnet unique and
// recoverable; date is the hidden CST timestamp ("" omits the span → zero published_at).
type rowSpec struct {
	id   int
	date string // "2006/01/02 15:04" in CST, or "" for no hidden date
}

// buildArchiveRow emits one `<tr class="">` carrying a recoverable id, a download-xl
// data-magnet, a 大小 size cell, and (optionally) a hidden CST date span.
func buildArchiveRow(r rowSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<tr class="">`)
	fmt.Fprintf(&b, `<td width="98">`)
	if r.date != "" {
		fmt.Fprintf(&b, `<span style="display: none;">%s</span>`, r.date)
	}
	fmt.Fprintf(&b, `</td>`)
	fmt.Fprintf(&b, `<td class="title"><a href="/topics/view/%d_synthetic.html" target="_blank">synthetic title %d</a></td>`, r.id, r.id)
	fmt.Fprintf(&b, `<td nowrap="nowrap" align="center">`)
	fmt.Fprintf(&b, `<a class="download-xl" title="迅雷下載" href="javascript:void(0);" data-magnet="magnet:?xt=urn:btih:%040x" onclick="downloadWithXunlei(this);">x</a>`, r.id)
	fmt.Fprintf(&b, `</td>`)
	fmt.Fprintf(&b, `<td nowrap="nowrap" align="center">3.6GB</td>`)
	fmt.Fprintf(&b, `<td align="center"><a href="/topics/list/user_id/1">synth</a></td>`)
	fmt.Fprintf(&b, `</tr>`)
	return b.String()
}

// buildArchivePage emits a structurally-valid DMHY archive listing page (carrying the
// id="topic_list" anchor) wrapping the given rows. Zero rows emits a legitimately-empty
// floor page (anchor present, no content rows).
func buildArchivePage(rows []rowSpec) []byte {
	var b strings.Builder
	b.WriteString(`<html><body><table id="topic_list"><tbody>`)
	b.WriteString(`<tr><th>header</th></tr>`)
	for _, r := range rows {
		b.WriteString(buildArchiveRow(r))
	}
	b.WriteString(`</tbody></table></body></html>`)
	return []byte(b.String())
}

// synthRows builds n rows with sequential ids starting at start, all carrying date.
func synthRows(start, n int, date string) []rowSpec {
	rows := make([]rowSpec, n)
	for i := 0; i < n; i++ {
		rows[i] = rowSpec{id: start + i, date: date}
	}
	return rows
}

// scriptedFetcher serves a SCRIPTED list of distinct page bodies by 1-based page number
// and records every page requested. A page past the list (or < 1) returns a structurally
// -valid empty floor page so the consecutive-empty terminator can resolve.
type scriptedFetcher struct {
	pages     [][]byte
	requested []int
}

func (s *scriptedFetcher) fetch(ctx context.Context, sortID, page int) ([]byte, error) {
	s.requested = append(s.requested, page)
	if page < 1 || page > len(s.pages) {
		return buildArchivePage(nil), nil
	}
	return s.pages[page-1], nil
}

// postSourceIDs returns the SourceID of every post (the recoverable identity).
func postSourceIDs(posts []rawpost.RawPost) []string {
	ids := make([]string, len(posts))
	for i, p := range posts {
		ids[i] = p.SourceID
	}
	return ids
}

// ---------------------------------------------------------------------------
// Fixture-manifest successor (TestP0_DMHyFixtureManifest).
// ---------------------------------------------------------------------------

var sizeCell = regexp.MustCompile(`align="center">[0-9.]+GB<`)

// TestP0_FixtureManifest asserts the copied DMHY testdata carries every named edge
// case the crawler parse / page-walk / threshold tests below depend on, so they are
// satisfiable offline from this module's own testdata/.
func TestP0_FixtureManifest(t *testing.T) {
	floor := loadFloor(t)
	n := floor.ConsecutiveEmptyThreshold

	t.Run("floor.json", func(t *testing.T) {
		if floor.Sources.Dmhy.SortID31.FloorPage == nil || *floor.Sources.Dmhy.SortID31.FloorPage <= 0 {
			t.Fatalf("%s: sort_id_31.floor_page must be a positive int", floorPth)
		}
		if n <= 0 {
			t.Fatalf("%s: consecutive_empty_threshold = %d, want a positive int N", floorPth, n)
		}
		if floor.CrawlRateRPS <= 0 {
			t.Fatalf("%s: crawl_rate_rps = %v, want > 0", floorPth, floor.CrawlRateRPS)
		}
	})

	t.Run("page-real-has-rows-and-size-cells", func(t *testing.T) {
		b := loadHTMLBytes(t, "page-real.html")
		rows := pageRows(b)
		if rows <= 0 {
			t.Fatalf("page-real.html: %d content rows, want > 0", rows)
		}
		if cells := sizeCell.FindAllString(string(b), -1); len(cells) < rows {
			t.Fatalf("page-real.html: %d 大小 GB size-cells for %d rows, want >= rows", len(cells), rows)
		}
	})

	t.Run("floor-empty-has-zero-rows", func(t *testing.T) {
		if rows := pageRows(loadHTMLBytes(t, "floor-empty.html")); rows != 0 {
			t.Fatalf("floor-empty.html: %d content rows, want 0 (empty-but-paginated floor)", rows)
		}
		// A legitimately-empty floor still carries the topic_list listing-table anchor —
		// that is what distinguishes it from a non-archive 200 (F0-3).
		if !strings.Contains(string(loadHTMLBytes(t, "floor-empty.html")), `id="topic_list"`) {
			t.Fatalf("floor-empty.html: missing id=\"topic_list\" anchor; a real empty floor must carry the listing table")
		}
	})

	t.Run("non-archive-200-has-zero-rows-and-no-anchor", func(t *testing.T) {
		b := loadHTMLBytes(t, "non-archive-200.html")
		if rows := pageRows(b); rows != 0 {
			t.Fatalf("non-archive-200.html: %d content rows, want 0 (a malformed/non-archive page)", rows)
		}
		if strings.Contains(string(b), `id="topic_list"`) {
			t.Fatalf("non-archive-200.html: carries id=\"topic_list\"; the non-archive fixture must LACK the listing-table anchor")
		}
	})

	t.Run("seq-fixtures-shape", func(t *testing.T) {
		guard := seqPages(t, "seq-guard", n)
		for i := 0; i < n-1; i++ {
			if pageRows(guard[i]) != 0 {
				t.Fatalf("seq-guard page-%d: want 0 rows (N-1 leading blanks)", i+1)
			}
		}
		if pageRows(guard[n-1]) <= 0 {
			t.Fatalf("seq-guard page-%d: want > 0 rows (Nth page non-empty so the threshold does not trip)", n)
		}
		term := seqPages(t, "seq-terminate", n)
		for i := 0; i < n; i++ {
			if pageRows(term[i]) != 0 {
				t.Fatalf("seq-terminate page-%d: want 0 rows (N leading blanks)", i+1)
			}
		}
	})

	// The synthetic builder must round-trip through ParseArchivePage, since the
	// clamp/offset cases below depend on it to emit observable rows.
	t.Run("synthetic-builder-parses", func(t *testing.T) {
		posts, err := ParseArchivePage(buildArchivePage(synthRows(1, 3, "2026/06/20 12:00")))
		if err != nil {
			t.Fatalf("ParseArchivePage(synthetic): %v", err)
		}
		if len(posts) != 3 {
			t.Fatalf("synthetic page emitted %d posts, want 3", len(posts))
		}
		for i, p := range posts {
			if p.SizeBytes <= 0 {
				t.Fatalf("synthetic row %d: SizeBytes=%d, want > 0", i, p.SizeBytes)
			}
			if !strings.Contains(p.Magnet, "magnet:") {
				t.Fatalf("synthetic row %d: magnet=%q, want a magnet URI", i, p.Magnet)
			}
			if p.SourceID == "" {
				t.Fatalf("synthetic row %d: empty SourceID", i)
			}
			if p.PublishedAt.IsZero() {
				t.Fatalf("synthetic row %d: zero PublishedAt despite a hidden date", i)
			}
		}
		// An empty synthetic page is a structurally-valid floor (anchor, zero rows).
		if posts, err := ParseArchivePage(buildArchivePage(nil)); err != nil || len(posts) != 0 {
			t.Fatalf("ParseArchivePage(empty synthetic) = (%d, %v), want (0, nil)", len(posts), err)
		}
	})
}

// ---------------------------------------------------------------------------
// HTML parse — ParseArchivePage field emission (the RSS→HTML size win).
// ---------------------------------------------------------------------------

// TestP1_CrawlerParsesArchivePage drives the HTML archive parser (ParseArchivePage)
// over the real page fixture and pins the field the whole RSS→HTML migration exists to
// protect: a real archive page carries 大小 sizes, so at least one emitted post must
// have SizeBytes > 0 (RSS reported size=0). It spot-checks the other core RawPost fields on
// that same post to prove the dumb crawler emits a fully-populated raw post.
func TestP1_CrawlerParsesArchivePage(t *testing.T) {
	posts, err := ParseArchivePage(loadHTMLBytes(t, "page-real.html"))
	if err != nil {
		t.Fatalf("ParseArchivePage(page-real.html): %v", err)
	}
	if len(posts) == 0 {
		t.Fatalf("page-real.html: emitted 0 posts, want > 0 (the archive page has content rows)")
	}

	var sized *rawpost.RawPost
	for i := range posts {
		if posts[i].SizeBytes > 0 {
			sized = &posts[i]
			break
		}
	}
	if sized == nil {
		t.Fatalf("no emitted post has SizeBytes > 0; HTML page 1 must carry real 大小 sizes (the reason RSS, which reports size=0, was dropped)")
	}

	if !strings.Contains(sized.Magnet, "magnet:") {
		t.Fatalf("post magnet = %q, want a magnet: URI", sized.Magnet)
	}
	if !strings.Contains(sized.Magnet, "&tr=") {
		t.Fatalf("post magnet = %q, want tracker-rich arrow magnet", sized.Magnet)
	}
	if strings.TrimSpace(sized.Title) == "" {
		t.Fatalf("post title is empty; the crawler must extract the raw title")
	}
	if strings.TrimSpace(sized.SourceID) == "" {
		t.Fatalf("post source_id is empty; the crawler must extract the source id")
	}
	if sized.Source != rawpost.SourceDMHY {
		t.Fatalf("post source = %q, want %q", sized.Source, rawpost.SourceDMHY)
	}
}

func TestP1_RowMagnetPrefersTrackerLink(t *testing.T) {
	row := `<a class="download-arrow arrow-magnet" href="magnet:?xt=urn:btih:BASE32HASH&dn=&tr=http%3A%2F%2Ftracker.example%2Fannounce">&nbsp;</a>` +
		`<a class="download-xl" data-magnet="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567">x</a>`

	if got := rowMagnet(row); !strings.Contains(got, "&tr=") {
		t.Fatalf("rowMagnet() = %q, want tracker-rich arrow magnet", got)
	}
}

// ---------------------------------------------------------------------------
// ParseSize size golden (successor to the root suite's ParseSize subtest).
// ---------------------------------------------------------------------------

// TestP1_ParseSizeGolden is sources/dmhy's own size golden: 3.6GB → 3,600,000,000
// bytes (decimal SI, 1 GB = 10^9). The HTML 大小 column is the only place size is
// parsed, so this golden lives with the crawler.
func TestP1_ParseSizeGolden(t *testing.T) {
	got, err := ParseSize("3.6GB")
	if err != nil {
		t.Fatalf("ParseSize(3.6GB): %v", err)
	}
	const want = int64(3_600_000_000)
	if got != want {
		t.Fatalf("ParseSize(3.6GB) = %d, want %d (decimal SI, 1GB = 10^9)", got, want)
	}
}

// ---------------------------------------------------------------------------
// /crawl page-walk: archive-floor threshold + resume (successors to TestP4_Backfill_*).
// ---------------------------------------------------------------------------

// noLimit is the lookback for a walk with no time bound (only the archive floor stops it).
const noLimit = time.Duration(0)

// TestP4_Crawl_ConsecutiveEmptyThreshold drives the page-walk over the two committed
// fixture sequences, with N read from floor.json. A single /crawl resolves the empty
// run before returning:
//
//   - seq-guard (N-1 empty pages then a NON-empty page): one /crawl walks THROUGH the
//     leading blanks into the content page, resetting the empty run — it returns
//     posts + a next_cursor and has_more (NOT a confirmed floor). It must NOT park at
//     has_more=false with a cursor inside the unresolved empty run.
//   - seq-terminate (N empty pages): one /crawl confirms the threshold and returns
//     has_more=false with an empty next_cursor.
func TestP4_Crawl_ConsecutiveEmptyThreshold(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	t.Run("guard-walks-through-blanks-to-content", func(t *testing.T) {
		pages := seqPages(t, "seq-guard", n)
		c := NewCrawler(seqFetcher(pages), n)
		// seq-guard ends on a content page that the fetcher repeats forever, so a content
		// stream has no floor — bound it with page_size=1 so the test terminates. The
		// load-bearing assertion is that the N-1 leading blanks do NOT trip the threshold:
		// one /crawl walks THROUGH them into content and returns posts + a cursor at the
		// content page, never a confirmed floor and never a cursor parked inside the run.
		resp, err := c.Crawl(ctx, CrawlRequest{PageSize: 1}, noLimit)
		if err != nil {
			t.Fatalf("Crawl(seq-guard): %v", err)
		}
		if !resp.HasMore {
			t.Fatalf("seq-guard: has_more = false; N-1 blanks then a non-empty page must NOT confirm the floor (N=%d)", n)
		}
		if len(resp.Posts) == 0 {
			t.Fatalf("seq-guard: 0 posts; the non-empty page after the blanks must yield posts")
		}
		if resp.NextCursor == "" {
			t.Fatalf("seq-guard: empty next_cursor with has_more=true; a continuing crawl must hand back a cursor")
		}
		// The cursor must point AT the content page (page >= N), never parked inside the
		// leading empty run (pages < N) — that would restart the counter at zero next call.
		gotPage, _, perr := parseCursor(resp.NextCursor)
		if perr != nil {
			t.Fatalf("seq-guard: next_cursor %q does not decode: %v", resp.NextCursor, perr)
		}
		if gotPage < n {
			t.Fatalf("seq-guard: next_cursor page = %d, want >= %d (cursor at the resolved content page, never inside the empty run)", gotPage, n)
		}
	})

	t.Run("terminate-confirms-archive-floor", func(t *testing.T) {
		pages := seqPages(t, "seq-terminate", n)
		// Count fetches: the terminator must trip EXACTLY as the Nth consecutive blank is
		// confirmed — neither earlier nor later. seqFetcher repeats the last empty page
		// forever, so an off-by-one impl requiring N+1 blanks still terminates with the
		// identical (has_more=false, "", 0 posts) shape; only the fetch count distinguishes
		// it, and an extra fetch is a wasted DMHY request past the archive floor.
		var fetched int
		seq := seqFetcher(pages)
		counting := func(ctx context.Context, sortID, page int) ([]byte, error) {
			fetched++
			return seq(ctx, sortID, page)
		}
		c := NewCrawler(PageFetcher(counting), n)
		resp, err := c.Crawl(ctx, CrawlRequest{}, noLimit)
		if err != nil {
			t.Fatalf("Crawl(seq-terminate): %v", err)
		}
		if resp.HasMore {
			t.Fatalf("seq-terminate: has_more = true; N=%d consecutive empty pages must positively confirm the floor", n)
		}
		if resp.NextCursor != "" {
			t.Fatalf("seq-terminate: next_cursor = %q, want empty at the floor (no parked cursor)", resp.NextCursor)
		}
		if len(resp.Posts) != 0 {
			t.Fatalf("seq-terminate: %d posts over all-empty pages, want 0", len(resp.Posts))
		}
		if fetched != n {
			t.Fatalf("seq-terminate: made %d fetches, want exactly N=%d (terminate as soon as the Nth consecutive blank confirms the floor)", fetched, n)
		}
	})
}

// TestP4_Crawl_ResumesFromCursor asserts the /crawl resume contract WITHOUT any cursor
// durability state (n8n persists next_cursor). Calling /crawl with a cursor for an
// already-walked page must make the NEXT call's first fetched page strictly greater than
// the cursor's page (resume past, never re-crawl from 1).
func TestP4_Crawl_ResumesFromCursor(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	page := loadHTMLBytes(t, "page-real.html")
	if pageRows(page) <= 0 {
		t.Fatalf("page-real.html parsed to 0 content rows; the resume fixture must be non-empty")
	}
	pages := [][]byte{page, page, page, page, page}

	var requested []int
	fetch := func(ctx context.Context, sortID, p int) ([]byte, error) {
		requested = append(requested, p)
		idx := p
		if idx < 1 {
			idx = 1
		}
		if idx > len(pages) {
			idx = len(pages)
		}
		return pages[idx-1], nil
	}
	c := NewCrawler(PageFetcher(fetch), n)

	// Resume from cursor (page 2, offset 0): page 2 was fully consumed, so the first fetch
	// of THIS call must be page 3 (strictly > 2). page_size=1 so the walk does not run on.
	cursor := formatCursor(2, 0)
	resp, err := c.Crawl(ctx, CrawlRequest{Cursor: cursor, PageSize: 1}, noLimit)
	if err != nil {
		t.Fatalf("Crawl(resume from %q): %v", cursor, err)
	}
	if len(requested) == 0 {
		t.Fatalf("resume Crawl never requested a page; resuming must drive a real fetch")
	}
	if first := requested[0]; first <= 2 {
		t.Fatalf("resumed at page %d; resuming from cursor %q must continue PAST page 2, not re-crawl from the top", first, cursor)
	}
	// page-real.html has 3 rows; page_size=1 fills mid-page → resume on the SAME page 3 at
	// offset 1.
	gotPage, gotOffset, perr := parseCursor(resp.NextCursor)
	if perr != nil {
		t.Fatalf("resume next_cursor %q does not decode: %v", resp.NextCursor, perr)
	}
	if gotPage < 3 {
		t.Fatalf("resume next_cursor page = %d, want >= 3 (resumed past the cursor page)", gotPage)
	}
	if gotPage == 3 && gotOffset != 1 {
		t.Fatalf("resume next_cursor = (%d,%d), want (3,1) (mid-page after taking 1 of 3 rows)", gotPage, gotOffset)
	}
}

// ---------------------------------------------------------------------------
// Empty-run continuity + transient error (never-done invariants).
// ---------------------------------------------------------------------------

// TestP4_Crawl_EmptyRunContinuity asserts a FRESH crawler resolves an empty run across
// N empty pages within a single /crawl (the in-process counter does NOT cross the
// request boundary). seq-terminate (N empty pages) confirms the threshold in one call;
// the cursor is never parked mid-run.
func TestP4_Crawl_EmptyRunContinuity(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()
	pages := seqPages(t, "seq-terminate", n)

	c := NewCrawler(seqFetcher(pages), n)
	resp, err := c.Crawl(ctx, CrawlRequest{}, noLimit)
	if err != nil {
		t.Fatalf("Crawl(empty run): %v", err)
	}
	if resp.HasMore {
		t.Fatalf("fresh crawler did NOT confirm the floor across %d empty pages in one request; the empty run must be resolved within a single /crawl", n)
	}
	if resp.NextCursor != "" {
		t.Fatalf("floor with a non-empty next_cursor %q; a confirmed floor parks no cursor", resp.NextCursor)
	}
}

// TestP4_Crawl_PageSizeBoundsContentStream asserts the page_size budget bounds the
// content walk: a stream of content pages with page_size=2 stops once 2 posts are
// accumulated and hands back a next_cursor (has_more=true) parked OUTSIDE any empty run.
// (This replaces the old MaxPagesCapsContentFetches: page_size is the content bound now.)
func TestP4_Crawl_PageSizeBoundsContentStream(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	// One row per page, content forever. page_size=2 stops after 2 rows = 2 pages.
	page1 := buildArchivePage([]rowSpec{{id: 101, date: "2026/06/20 12:00"}})
	page2 := buildArchivePage([]rowSpec{{id: 102, date: "2026/06/20 11:00"}})
	sf := &scriptedFetcher{pages: [][]byte{page1, page2, page2, page2}}
	c := NewCrawler(PageFetcher(sf.fetch), n)

	resp, err := c.Crawl(ctx, CrawlRequest{PageSize: 2}, noLimit)
	if err != nil {
		t.Fatalf("Crawl(page_size=2 content stream): %v", err)
	}
	if !resp.HasMore {
		t.Fatalf("content stream reported has_more=false; a non-empty stream within budget has more to fetch")
	}
	if len(resp.Posts) != 2 {
		t.Fatalf("returned %d posts, want exactly 2 (page_size budget bounds content)", len(resp.Posts))
	}
	if resp.NextCursor == "" {
		t.Fatalf("has_more=true with empty next_cursor; a continuing crawl must hand back a cursor outside any empty run")
	}
	// Budget filled exactly at page 2's last (only) row → park (2,0) so resume fetches page 3.
	gotPage, gotOffset, perr := parseCursor(resp.NextCursor)
	if perr != nil {
		t.Fatalf("next_cursor %q does not decode: %v", resp.NextCursor, perr)
	}
	if gotPage != 2 || gotOffset != 0 {
		t.Fatalf("next_cursor = (%d,%d), want (2,0) (budget filled at page 2's last row; resume fetches page 3)", gotPage, gotOffset)
	}
}

// errTransient is the injected transient fetch failure.
var errTransient = errors.New("dmhy-conformance: injected transient fetch failure")

// TestP4_Crawl_TransientErrorNeverTerminates injects a transient fetch failure and
// asserts /crawl surfaces it as a NON-NIL error and NEVER as the archive floor: the
// response is the zero CrawlResponse (no terminal shape, no parked cursor), and the
// error is the injected one propagated (not swallowed/relabeled as a floor).
func TestP4_Crawl_TransientErrorNeverTerminates(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	fetch := func(ctx context.Context, sortID, page int) ([]byte, error) {
		return nil, errTransient
	}
	c := NewCrawler(PageFetcher(fetch), n)

	resp, err := c.Crawl(ctx, CrawlRequest{}, noLimit)
	if err == nil {
		t.Fatalf("Crawl on a transient fetch failure returned nil error; a transient blip must surface as a non-nil error, never the floor")
	}
	if !errors.Is(err, errTransient) {
		t.Fatalf("Crawl returned err %v, want the injected transient error propagated (never swallowed/relabeled as the floor)", err)
	}
	if resp.HasMore {
		t.Fatalf("Crawl on a transient failure reported has_more=true; a failed fetch must NEVER look like a continuing walk")
	}
	if resp.NextCursor != "" || len(resp.Posts) != 0 {
		t.Fatalf("Crawl on a transient failure returned posts=%d next_cursor=%q, want the empty zero response", len(resp.Posts), resp.NextCursor)
	}
}

// TestP4_Crawl_NonArchivePageNeverTerminates feeds the page-walk a 200-OK page that
// parses to zero rows but is NOT a real archive listing (no topic_list anchor — a CDN
// interstitial / truncation / markup drift). It MUST surface as a non-nil error and
// NEVER as the floor: a swallowed parse failure counted toward the consecutive-empty
// threshold would silently truncate backfill at a fake floor (F0-3).
func TestP4_Crawl_NonArchivePageNeverTerminates(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	// First assert the unit parse contract: a non-archive 200 is an error, a real floor
	// page is not — both have zero content rows, so the anchor is the discriminator.
	if _, err := ParseArchivePage(loadHTMLBytes(t, "non-archive-200.html")); err == nil {
		t.Fatalf("ParseArchivePage(non-archive-200.html) returned nil error; a zero-row page lacking the topic_list anchor must surface as a parse error, never a legitimately-empty floor")
	}
	if posts, err := ParseArchivePage(loadHTMLBytes(t, "floor-empty.html")); err != nil || len(posts) != 0 {
		t.Fatalf("ParseArchivePage(floor-empty.html) = (%d posts, %v), want (0, nil) — a real empty floor parses cleanly", len(posts), err)
	}

	// Now the page-walk: a non-archive 200 repeated forever must NEVER trip the
	// consecutive-empty threshold. It surfaces as an error (502 at the HTTP boundary),
	// not has_more=false with a parked-empty cursor.
	page := loadHTMLBytes(t, "non-archive-200.html")
	pages := [][]byte{page, page, page, page}
	c := NewCrawler(seqFetcher(pages), n)
	resp, err := c.Crawl(ctx, CrawlRequest{}, noLimit)
	if err == nil {
		t.Fatalf("Crawl over non-archive 200 pages returned nil error; a malformed page must surface as a fetch failure, never the floor")
	}
	if resp.HasMore {
		t.Fatalf("Crawl over non-archive 200 pages reported has_more=true; a swallowed parse failure must NEVER look like a continuing walk (F0-3)")
	}
	if resp.NextCursor != "" || len(resp.Posts) != 0 {
		t.Fatalf("Crawl over non-archive 200 pages returned posts=%d next_cursor=%q, want the empty zero response", len(resp.Posts), resp.NextCursor)
	}
}

// ---------------------------------------------------------------------------
// NEW §1 contract: clamp, offset resume, page-boundary no-peek, lookback, zero-time.
// ---------------------------------------------------------------------------

// TestP4_Crawl_PageSizeClamp asserts page_size clamps to [1, 200]. The 0→1 / 1→1 floor
// is observable on a small page; the 200/500 ceiling needs a > 200-row page (F0-9), so
// the synthetic builder emits 250 rows and a page_size=500 request must return exactly
// 200 posts with the resume cursor mid-page at offset 200 (proving the clamp bit, not
// the page running out).
func TestP4_Crawl_PageSizeClamp(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	t.Run("zero-clamps-to-1", func(t *testing.T) {
		sf := &scriptedFetcher{pages: [][]byte{buildArchivePage(synthRows(1, 10, "2026/06/20 12:00"))}}
		c := NewCrawler(PageFetcher(sf.fetch), n)
		resp, err := c.Crawl(ctx, CrawlRequest{PageSize: 0}, noLimit)
		if err != nil {
			t.Fatalf("Crawl(page_size=0): %v", err)
		}
		if len(resp.Posts) != 1 {
			t.Fatalf("page_size=0 returned %d posts, want exactly 1 (clamp 0→1)", len(resp.Posts))
		}
	})

	t.Run("one-stays-1", func(t *testing.T) {
		sf := &scriptedFetcher{pages: [][]byte{buildArchivePage(synthRows(1, 10, "2026/06/20 12:00"))}}
		c := NewCrawler(PageFetcher(sf.fetch), n)
		resp, err := c.Crawl(ctx, CrawlRequest{PageSize: 1}, noLimit)
		if err != nil {
			t.Fatalf("Crawl(page_size=1): %v", err)
		}
		if len(resp.Posts) != 1 {
			t.Fatalf("page_size=1 returned %d posts, want exactly 1", len(resp.Posts))
		}
	})

	t.Run("500-clamps-to-200", func(t *testing.T) {
		// A single page with 250 in-window rows. page_size=500 clamps to 200 → exactly 200
		// posts, resume cursor mid-page at offset 200 (the clamp bit, not the page ending).
		sf := &scriptedFetcher{pages: [][]byte{buildArchivePage(synthRows(1, 250, "2026/06/20 12:00"))}}
		c := NewCrawler(PageFetcher(sf.fetch), n)
		resp, err := c.Crawl(ctx, CrawlRequest{PageSize: 500}, noLimit)
		if err != nil {
			t.Fatalf("Crawl(page_size=500): %v", err)
		}
		if len(resp.Posts) != 200 {
			t.Fatalf("page_size=500 returned %d posts, want exactly 200 (clamp 500→200)", len(resp.Posts))
		}
		if !resp.HasMore {
			t.Fatalf("page_size=500 over a 250-row page: has_more=false; 50 rows remain in-window")
		}
		gotPage, gotOffset, perr := parseCursor(resp.NextCursor)
		if perr != nil {
			t.Fatalf("next_cursor %q does not decode: %v", resp.NextCursor, perr)
		}
		if gotPage != 1 || gotOffset != 200 {
			t.Fatalf("next_cursor = (%d,%d), want (1,200) (resume mid-page after the clamped 200)", gotPage, gotOffset)
		}
	})
}

// TestP4_Crawl_MidPageOffsetResume serves a SINGLE page with > page_size in-window rows.
// The first /crawl fills the budget mid-page and MUST return next_cursor=(page, taken)
// with offset>0; the second /crawl with that cursor re-fetches the SAME page, skips
// exactly offset leading rows, and the UNION of SourceIDs across the two calls equals
// the page's rows up to the second budget with NO gap and NO overlap at the seam (an
// off-by-one drops or doubles the seam row). This pins the curOffset+taken expression.
func TestP4_Crawl_MidPageOffsetResume(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	page := buildArchivePage(synthRows(1, 20, "2026/06/20 12:00"))
	sf := &scriptedFetcher{pages: [][]byte{page}}
	c := NewCrawler(PageFetcher(sf.fetch), n)

	// First call: page_size=5 fills mid-page → offset must be 5.
	resp1, err := c.Crawl(ctx, CrawlRequest{PageSize: 5}, noLimit)
	if err != nil {
		t.Fatalf("Crawl(first, page_size=5): %v", err)
	}
	if len(resp1.Posts) != 5 || !resp1.HasMore {
		t.Fatalf("first call: %d posts has_more=%v, want 5 posts has_more=true", len(resp1.Posts), resp1.HasMore)
	}
	gotPage, gotOffset, perr := parseCursor(resp1.NextCursor)
	if perr != nil {
		t.Fatalf("first next_cursor %q does not decode: %v", resp1.NextCursor, perr)
	}
	if gotPage != 1 || gotOffset != 5 {
		t.Fatalf("first next_cursor = (%d,%d), want (1,5) (mid-page after taking 5)", gotPage, gotOffset)
	}

	// Second call: resume at (1,5), page_size=5 → rows 6..10.
	resp2, err := c.Crawl(ctx, CrawlRequest{Cursor: resp1.NextCursor, PageSize: 5}, noLimit)
	if err != nil {
		t.Fatalf("Crawl(second, resume): %v", err)
	}
	if len(resp2.Posts) != 5 {
		t.Fatalf("second call returned %d posts, want 5", len(resp2.Posts))
	}
	// The second call must re-fetch the SAME page (page 1), not advance.
	if last := sf.requested[len(sf.requested)-1]; last != 1 {
		t.Fatalf("second call last fetched page %d, want 1 (re-fetch the same page to skip offset rows)", last)
	}

	// Union of SourceIDs across both calls must be the first 10 rows, no gap, no overlap.
	union := append(append([]string{}, postSourceIDs(resp1.Posts)...), postSourceIDs(resp2.Posts)...)
	if len(union) != 10 {
		t.Fatalf("union has %d ids, want 10", len(union))
	}
	seen := map[string]bool{}
	for i, id := range union {
		if seen[id] {
			t.Fatalf("duplicate SourceID %q at union index %d — overlap at the mid-page seam", id, i)
		}
		seen[id] = true
	}
	for i := 1; i <= 10; i++ {
		want := strconv.Itoa(i)
		if !seen[want] {
			t.Fatalf("missing SourceID %q from the union — gap at the mid-page seam", want)
		}
	}
}

// TestP4_Crawl_PageBoundaryNoPeek asserts that when the budget fills EXACTLY at a page's
// last in-window row, the engine parks (page, 0) (NOT (page+1, 0) — F1-1) WITHOUT
// peeking the unfetched page+1, then the follow-up /crawl fetches page+1 FIRST (no
// page+2 skip).
func TestP4_Crawl_PageBoundaryNoPeek(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	// Page 1 has exactly 3 rows; page 2 onward is the empty floor (so the follow-up
	// resolves has_more=false). page_size=3 fills exactly at page 1's last row.
	page1 := buildArchivePage(synthRows(1, 3, "2026/06/20 12:00"))
	sf := &scriptedFetcher{pages: [][]byte{page1}}
	c := NewCrawler(PageFetcher(sf.fetch), n)

	resp1, err := c.Crawl(ctx, CrawlRequest{PageSize: 3}, noLimit)
	if err != nil {
		t.Fatalf("Crawl(first, page_size=3): %v", err)
	}
	if len(resp1.Posts) != 3 || !resp1.HasMore {
		t.Fatalf("first call: %d posts has_more=%v, want 3 posts has_more=true", len(resp1.Posts), resp1.HasMore)
	}
	gotPage, gotOffset, perr := parseCursor(resp1.NextCursor)
	if perr != nil {
		t.Fatalf("first next_cursor %q does not decode: %v", resp1.NextCursor, perr)
	}
	if gotPage != 1 || gotOffset != 0 {
		t.Fatalf("first next_cursor = (%d,%d), want (1,0) (budget at page 1's last row → resume fetches page 2, NOT page+1=2 mis-encoded as page 2 skip)", gotPage, gotOffset)
	}
	// page+1 (page 2) must NOT have been fetched on the FIRST call (no peek).
	for _, p := range sf.requested {
		if p > 1 {
			t.Fatalf("first call fetched page %d; page+1 must NOT be peeked when budget fills at a page boundary", p)
		}
	}

	// Follow-up: resume at (1,0) must fetch page 2 FIRST (no skip to page+2).
	before := len(sf.requested)
	resp2, err := c.Crawl(ctx, CrawlRequest{Cursor: resp1.NextCursor, PageSize: 3}, noLimit)
	if err != nil {
		t.Fatalf("Crawl(follow-up): %v", err)
	}
	if sf.requested[before] != 2 {
		t.Fatalf("follow-up first fetched page = %d, want 2 (resume from (1,0) fetches page 2, never skips to page 3 — the (page+1,0) mis-decode)", sf.requested[before])
	}
	if resp2.HasMore || resp2.NextCursor != "" || len(resp2.Posts) != 0 {
		t.Fatalf("follow-up over the floor: posts=%d has_more=%v cursor=%q, want (0, false, \"\")", len(resp2.Posts), resp2.HasMore, resp2.NextCursor)
	}
}

// TestP4_Crawl_LookbackBoundedStop pins the lookback cutoff with an injected clock. It
// straddles the boundary within < 8h of a fixture row's CST timestamp so a UTC-vs-CST
// misparse (F0-5) would flip the in/out verdict.
func TestP4_Crawl_LookbackBoundedStop(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	// page-real.html rows: 2026/06/20 22:25, 20:32, 20:19 (CST). Place the cutoff between
	// 20:32 and 20:19 CST: lookback window must INCLUDE 22:25 and 20:32, EXCLUDE 20:19.
	// cutoff = now − lookback. Pick now = 2026/06/20 22:30 CST, lookback = 2h5m →
	// cutoff = 20:25 CST. Rows 22:25 & 20:32 are after cutoff (in), 20:19 is before (out).
	cst := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 6, 20, 22, 30, 0, 0, cst)
	lookback := 2*time.Hour + 5*time.Minute

	// With the BUGGY UTC parse, the rows would read as 22:25/20:32/20:19 UTC = +8h later
	// instants, so against the same cutoff every row reads ~8h newer and ALL stay in-window
	// — the 20:19 row would NOT fall out, and this test's "stops before the last row" check
	// fails. The < 8h straddle is what makes the skew observable.
	page := loadHTMLBytes(t, "page-real.html")
	sf := &scriptedFetcher{pages: [][]byte{page}}
	c := NewCrawler(PageFetcher(sf.fetch), n)
	c.now = func() time.Time { return now }

	resp, err := c.Crawl(ctx, CrawlRequest{PageSize: 50, Lookback: "ignored-engine-takes-duration"}, lookback)
	if err != nil {
		t.Fatalf("Crawl(lookback): %v", err)
	}
	// page-real.html has 3 rows; the 3rd (20:19) is out-of-window → 2 posts, then stop.
	if len(resp.Posts) != 2 {
		t.Fatalf("lookback walk returned %d posts, want exactly 2 (22:25 & 20:32 in-window, 20:19 out — a UTC misparse keeps all 3 in-window)", len(resp.Posts))
	}
	if resp.HasMore {
		t.Fatalf("lookback boundary reached: has_more=true, want false")
	}
	if resp.NextCursor != "" {
		t.Fatalf("lookback boundary: next_cursor=%q, want empty", resp.NextCursor)
	}
}

// TestP4_Crawl_ZeroTimeKept asserts a zero/unparseable published_at is KEPT in-window: a
// page ordered in-window → zero-time (parse glitch) → out-of-window must emit BOTH the
// in-window AND the zero-time row and only stop at the trailing out-of-window row. This
// pins !t.IsZero() && t.Before(cutoff) against the naive t.Before(cutoff) regression
// (which would drop/stop on the zero-time row, since 0001-01-01 < any cutoff).
func TestP4_Crawl_ZeroTimeKept(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	cst := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 6, 20, 13, 0, 0, 0, cst)
	lookback := 2 * time.Hour // cutoff = 11:00 CST

	// Row order newest→oldest: in-window (12:00), zero-time (no date), out-of-window (10:00).
	rows := []rowSpec{
		{id: 1, date: "2026/06/20 12:00"}, // in-window (> 11:00)
		{id: 2, date: ""},                 // zero-time → kept
		{id: 3, date: "2026/06/20 10:00"}, // out-of-window (< 11:00) → stop
	}
	sf := &scriptedFetcher{pages: [][]byte{buildArchivePage(rows)}}
	c := NewCrawler(PageFetcher(sf.fetch), n)
	c.now = func() time.Time { return now }

	resp, err := c.Crawl(ctx, CrawlRequest{PageSize: 50}, lookback)
	if err != nil {
		t.Fatalf("Crawl(zero-time): %v", err)
	}
	if len(resp.Posts) != 2 {
		t.Fatalf("returned %d posts, want 2 (in-window + zero-time kept; out-of-window stops the walk)", len(resp.Posts))
	}
	ids := postSourceIDs(resp.Posts)
	if ids[0] != "1" || ids[1] != "2" {
		t.Fatalf("emitted ids = %v, want [1 2] (the in-window row then the kept zero-time row, NOT the out-of-window row)", ids)
	}
	if resp.HasMore {
		t.Fatalf("walk hit the out-of-window boundary: has_more=true, want false")
	}
}

// TestP4_Crawl_BudgetEdgeOnLookbackBoundary (F0-1, same-page peek). Serve a single page
// whose row at index page_size (the first UNRETURNED row) is out-of-window while the
// first page_size rows are in-window. The budget-filling /crawl returns exactly page_size
// posts but has_more=false, next_cursor="" — the engine peeked the next in-hand same-page
// row, saw it out-of-window, and reported the boundary instead of parking a dead cursor.
// The naive always-has_more=true-on-budget-fill regression fails here.
func TestP4_Crawl_BudgetEdgeOnLookbackBoundary(t *testing.T) {
	n := threshold(t)
	ctx := context.Background()

	cst := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 6, 20, 13, 0, 0, 0, cst)
	lookback := 2 * time.Hour // cutoff = 11:00 CST

	// 3 in-window rows then 1 out-of-window row. page_size=3 fills at row 3; the next
	// same-page row (row 4) is out-of-window → boundary at the budget edge.
	rows := []rowSpec{
		{id: 1, date: "2026/06/20 12:30"},
		{id: 2, date: "2026/06/20 12:20"},
		{id: 3, date: "2026/06/20 12:10"},
		{id: 4, date: "2026/06/20 10:00"}, // out-of-window
	}
	sf := &scriptedFetcher{pages: [][]byte{buildArchivePage(rows)}}
	c := NewCrawler(PageFetcher(sf.fetch), n)
	c.now = func() time.Time { return now }

	resp, err := c.Crawl(ctx, CrawlRequest{PageSize: 3}, lookback)
	if err != nil {
		t.Fatalf("Crawl(budget edge on boundary): %v", err)
	}
	if len(resp.Posts) != 3 {
		t.Fatalf("returned %d posts, want exactly 3 (the in-window budget)", len(resp.Posts))
	}
	if resp.HasMore {
		t.Fatalf("budget filled exactly at the lookback boundary: has_more=true, want false (the next same-page row is out-of-window)")
	}
	if resp.NextCursor != "" {
		t.Fatalf("budget-edge boundary parked a cursor %q; a dead cursor (zero in-window rows on re-issue) must not be parked", resp.NextCursor)
	}
}

// ---------------------------------------------------------------------------
// POST /crawl HTTP boundary: method routing, status mapping, JSON shape, round-trip.
// ---------------------------------------------------------------------------

// crawlServer mounts the real Server.ServeHTTP at /crawl over an injected fetcher, so
// the boundary tests exercise the genuine handler (routing + status mapping + JSON
// encode) against deterministic offline fixtures, no network.
func crawlServer(t *testing.T, fetch PageFetcher, n int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/crawl", newServerWithFetcher(fetch, n))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestP4_CrawlHTTP_MethodRouting asserts the handler rejects non-POST with 405.
func TestP4_CrawlHTTP_MethodRouting(t *testing.T) {
	ts := crawlServer(t, func(context.Context, int, int) ([]byte, error) {
		t.Fatalf("fetcher must not be reached on a rejected method")
		return nil, nil
	}, threshold(t))

	resp, err := http.Get(ts.URL + "/crawl")
	if err != nil {
		t.Fatalf("GET /crawl: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /crawl status = %d, want 405 (only POST is routed)", resp.StatusCode)
	}
}

// TestP4_CrawlHTTP_BadBodyIs400 asserts a malformed JSON body maps to 400, never 502.
func TestP4_CrawlHTTP_BadBodyIs400(t *testing.T) {
	ts := crawlServer(t, func(context.Context, int, int) ([]byte, error) {
		t.Fatalf("fetcher must not be reached on a malformed request body")
		return nil, nil
	}, threshold(t))

	resp, err := http.Post(ts.URL+"/crawl", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("POST /crawl: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /crawl with malformed body status = %d, want 400", resp.StatusCode)
	}
}

// TestP4_CrawlHTTP_BadParamIs400 (F1-2 — pins the 400 fix over the wire). A well-formed
// JSON body carrying a malformed lookback, and a separate one carrying a malformed
// cursor, must each map to 400 — never 502 or 200. This is the ONLY case driving the
// real ServeHTTP 400 branch end-to-end; without it an implementer could leave
// lookback/cursor parsing in the engine (→ 502) and ship a transient-looking error for a
// permanently-bad param.
func TestP4_CrawlHTTP_BadParamIs400(t *testing.T) {
	ts := crawlServer(t, func(context.Context, int, int) ([]byte, error) {
		t.Fatalf("fetcher must not be reached on a malformed client param")
		return nil, nil
	}, threshold(t))

	t.Run("bad-lookback", func(t *testing.T) {
		body, _ := json.Marshal(CrawlRequest{PageSize: 10, Lookback: "5x"})
		resp, err := http.Post(ts.URL+"/crawl", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("POST /crawl: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("POST /crawl with malformed lookback status = %d, want 400 (a bad param is a client error, never a 502 upstream failure)", resp.StatusCode)
		}
	})

	t.Run("bad-cursor", func(t *testing.T) {
		body, _ := json.Marshal(CrawlRequest{PageSize: 10, Cursor: "abc"})
		resp, err := http.Post(ts.URL+"/crawl", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("POST /crawl: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("POST /crawl with malformed cursor status = %d, want 400", resp.StatusCode)
		}
	})
}

// TestP4_CrawlHTTP_FetchFailureIs502 asserts a crawl (fetch) failure maps to 502 — a
// transient upstream failure, never the floor and never a 200.
func TestP4_CrawlHTTP_FetchFailureIs502(t *testing.T) {
	ts := crawlServer(t, func(context.Context, int, int) ([]byte, error) {
		return nil, errTransient
	}, threshold(t))

	body, _ := json.Marshal(CrawlRequest{PageSize: 10})
	resp, err := http.Post(ts.URL+"/crawl", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /crawl: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("POST /crawl with a fetch failure status = %d, want 502 (transient upstream, never the floor)", resp.StatusCode)
	}
}

// TestP4_CrawlHTTP_SuccessShape asserts a POST /crawl over the HTML archive walk returns
// 200 with Content-Type application/json and a JSON body that decodes back into the
// parsed posts — the params→posts round-trip across the wire. The fetcher returns the
// real HTML archive page, bounded to one page_size budget so the walk terminates without
// running to the floor.
func TestP4_CrawlHTTP_SuccessShape(t *testing.T) {
	page := loadHTMLBytes(t, "page-real.html")
	want, err := ParseArchivePage(page)
	if err != nil {
		t.Fatalf("ParseArchivePage: %v", err)
	}
	if len(want) == 0 {
		t.Fatalf("page-real.html parsed to 0 posts; the success-shape fixture must be non-empty")
	}
	ts := crawlServer(t, func(_ context.Context, _ int, _ int) ([]byte, error) {
		return page, nil
	}, threshold(t))

	// page_size == row count returns the whole page; the next page (repeat) keeps the walk
	// from the floor — but page_size bounds it at exactly len(want) here.
	reqBody, _ := json.Marshal(CrawlRequest{PageSize: len(want)})
	resp, err := http.Post(ts.URL+"/crawl", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /crawl: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /crawl status = %d, want 200; body=%s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("POST /crawl Content-Type = %q, want application/json", ct)
	}
	var got CrawlResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode /crawl response: %v", err)
	}
	if len(got.Posts) != len(want) {
		t.Fatalf("/crawl returned %d posts, want %d (params→posts round-trip)", len(got.Posts), len(want))
	}
}
