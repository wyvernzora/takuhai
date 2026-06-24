package dmhy

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/wyvernzora/takuhai/pkg/rawpost"
)

// archiveRowRe splits a DMHY archive page's <tbody> into content rows. A content row
// opens with `<tr class="">` (the same selector the fixture manifest gate and the
// resume/threshold fixtures key on); the thead's header row is a bare `<tr>` and is
// not matched. A page with none of these parses to zero rows — the empty-but-
// paginated floor page the threshold terminator positively confirms.
var archiveRowRe = regexp.MustCompile(`<tr class="">`)

// listTableRe is the structural anchor proving the bytes are a real DMHY archive
// listing page: the `<table ... id="topic_list">` container that wraps the topic
// table on EVERY listing page — content pages, floor-empty pages, and the empty
// sequence pages all carry it. A 200 OK that does NOT carry it is a non-archive page
// (CDN interstitial, truncation, markup drift), NOT a legitimately-empty floor, so a
// zero-row page is only the floor when this anchor is present.
var listTableRe = regexp.MustCompile(`id="topic_list"`)

// errNonArchivePage is returned when a 200-OK page parses to zero content rows AND
// lacks the listing-table anchor — a malformed/non-archive page the page-walk must
// surface as a fetch failure, never count toward the consecutive-empty floor
// threshold (a swallowed parse failure would falsely truncate backfill).
var errNonArchivePage = errors.New("dmhy: archive page has no topic_list table (non-archive 200: interstitial, truncation, or markup drift) — not a legitimately-empty floor")

// titleCellRe / anchorRe pull the topic href and the raw display title out of a
// row's `<td class="title">` anchor.
var (
	titleCellRe = regexp.MustCompile(`(?s)<td class="title">(.*?)</td>`)
	anchorRe    = regexp.MustCompile(`(?s)<a[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
)

// arrowMagnetRe pulls the tracker-rich magnet out of the normal download link. Its
// btih may be 32-char base32; takuhai decodes it on /ingest. dataMagnetRe is the
// fallback Xunlei magnet, which is usually bare hex with no trackers.
var (
	dataMagnetRe  = regexp.MustCompile(`data-magnet="(magnet:[^"]*)"`)
	arrowMagnetRe = regexp.MustCompile(`class="[^"]*arrow-magnet"[^>]*href="(magnet:[^"]*)"`)
)

// bfSizeCellRe pulls a row's 大小 size token out of its `align="center">...</td>` size
// cell (e.g. "3.6GB"). HTML carries size, unlike RSS (design §3/§8).
var bfSizeCellRe = regexp.MustCompile(`align="center">([0-9.]+\s*[KMGT]?B)<`)

// hiddenDateRe pulls the row's hidden machine-readable timestamp
// (`<span style="display: none;">2026/06/20 22:25</span>`); the visible cell uses
// relative wording ("今天 22:25") that is not parseable.
var hiddenDateRe = regexp.MustCompile(`display:\s*none;?">\s*([0-9]{4}/[0-9]{2}/[0-9]{2} [0-9]{2}:[0-9]{2})`)

// ParseArchivePage parses a DMHY HTML-archive page's content rows into raw posts. It is
// the crawler's exported HTML primitive — the page-walk uses it live, and the
// `takuhai-dmhy parse` backfill command reuses it on locally-saved pages. It is the
// DUMB-crawler successor of internal/source/dmhy/backfill.go's
// parseArchivePage: it emits EVERY content row as a rawpost.RawPost (raw
// title+magnet+metadata+size), including rows whose magnet has no canonical v1 btih
// (pure-v2 / malformed) — those were SKIPPED by the old parser but are now emitted
// here with their raw magnet and no infohash. takuhai derives the dedup key and the
// skipped bucket on /ingest.
//
// End-of-archive detection rides on the row count, but ONLY for pages that are
// structurally real archive listings: a page with zero `<tr class="">` rows is the
// floor page (returns zero posts, nil error) ONLY when it carries the topic_list
// table anchor. A zero-row page that LACKS that anchor is a non-archive 200 (CDN
// interstitial, truncation, markup drift) and returns errNonArchivePage so the
// page-walk surfaces it as a fetch failure — never counting it toward the
// consecutive-empty floor threshold (a swallowed parse failure would silently
// truncate backfill at a fake floor, the inverse of what the threshold prevents).
func ParseArchivePage(html []byte) ([]rawpost.RawPost, error) {
	body := string(html)

	// Locate each content row by its opening `<tr class="">` and bound it at the next
	// content row (or end of input). This is the same selector the fixture manifest and
	// the threshold sequence fixtures count on, so the row count here matches the
	// offline reference exactly.
	starts := archiveRowRe.FindAllStringIndex(body, -1)
	if len(starts) == 0 {
		// Zero content rows. Distinguish a legitimately-empty floor (carries the listing
		// table) from a malformed/non-archive 200 (lacks it) — only the former is a real
		// empty page the threshold may count.
		if !listTableRe.MatchString(body) {
			return nil, errNonArchivePage
		}
		return nil, nil
	}

	posts := make([]rawpost.RawPost, 0, len(starts))
	for i, loc := range starts {
		end := len(body)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		row := body[loc[0]:end]

		href, title := rowTitle(row)
		var size int64
		if m := bfSizeCellRe.FindStringSubmatch(row); m != nil {
			if n, err := ParseSize(m[1]); err == nil {
				size = n
			}
		}

		magnet := rowMagnet(row)
		sid := rowSourceID(href)

		posts = append(posts, rawpost.RawPost{
			Source:      rawpost.SourceDMHY,
			SourceID:    sid,
			URL:         href,
			Title:       title,
			Magnet:      magnet,
			PublishedAt: rowPublishedAt(row),
			SizeBytes:   size,
		})
	}
	return posts, nil
}

// rowMagnet returns the representative magnet for a row: prefer the normal
// arrow-magnet because DMHY puts tracker params there, falling back to the bare
// data-magnet when no arrow link exists.
func rowMagnet(row string) string {
	if m := arrowMagnetRe.FindStringSubmatch(row); m != nil {
		return m[1]
	}
	if m := dataMagnetRe.FindStringSubmatch(row); m != nil {
		return m[1]
	}
	return ""
}

// rowTitle extracts the topic href and the raw display title from a row's
// `<td class="title">` anchor.
func rowTitle(row string) (href, title string) {
	cell := titleCellRe.FindStringSubmatch(row)
	if cell == nil {
		return "", ""
	}
	a := anchorRe.FindStringSubmatch(cell[1])
	if a == nil {
		return "", ""
	}
	return a[1], strings.TrimSpace(a[2])
}

// rowSourceID derives the source-native stable id from the topic href
// (`/topics/view/721238_...html` -> "721238"), so backfilled posts collapse onto the
// same raw_items row as a re-scrape of the same topic. Falls back to the full href.
func rowSourceID(href string) string {
	const marker = "/topics/view/"
	i := strings.Index(href, marker)
	if i < 0 {
		return strings.TrimSpace(href)
	}
	rest := href[i+len(marker):]
	if j := strings.IndexByte(rest, '_'); j >= 0 {
		return rest[:j]
	}
	if j := strings.IndexByte(rest, '.'); j >= 0 {
		return rest[:j]
	}
	return rest
}

// dmhyZone is the DMHY hidden-timestamp zone (CST, UTC+8). The hidden date spans carry
// no zone, so parsing them as UTC would skew the lookback window by ~8h; parse them in
// the DMHY zone so PublishedAt is a correct instant.
var dmhyZone = time.FixedZone("CST", 8*3600)

// rowPublishedAt parses the row's hidden timestamp; a missing/unparseable date yields
// the zero time (the post is still emitted — takuhai tolerates it). The hidden span is
// CST (UTC+8), so it is parsed in dmhyZone to produce a correct instant.
func rowPublishedAt(row string) time.Time {
	m := hiddenDateRe.FindStringSubmatch(row)
	if m == nil {
		return time.Time{}
	}
	if t, err := time.ParseInLocation("2006/01/02 15:04", strings.TrimSpace(m[1]), dmhyZone); err == nil {
		return t
	}
	return time.Time{}
}
