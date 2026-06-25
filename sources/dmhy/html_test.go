package dmhy

import (
	"testing"
	"time"
)

// TestRowPublishedAtCST pins the DMHY hidden-timestamp zone (CST, UTC+8). A hidden span
// "2026/06/20 12:00" is a CST instant, i.e. 2026/06/20 04:00 UTC — NOT 12:00 UTC. A
// zone-less parse (the prior bug) would skew the lookback window by ~8h. This is the
// regression a self-consistent fixture-through-same-parser path cannot catch.
func TestRowPublishedAtCST(t *testing.T) {
	row := `<tr class=""><span style="display: none;">2026/06/20 12:00</span></tr>`
	got := rowPublishedAt(row)

	want := time.Date(2026, 6, 20, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	if !got.Equal(want) {
		t.Fatalf("rowPublishedAt = %v (UTC %v), want %v (UTC %v)",
			got, got.UTC(), want, want.UTC())
	}
	// Explicit: the instant is 04:00 UTC, not 12:00 UTC.
	wantUTC := time.Date(2026, 6, 20, 4, 0, 0, 0, time.UTC)
	if !got.UTC().Equal(wantUTC) {
		t.Fatalf("rowPublishedAt UTC = %v, want %v (CST 12:00 = UTC 04:00)", got.UTC(), wantUTC)
	}
}

// TestRowPublishedAtMissing confirms a missing/unparseable hidden date yields the zero
// time (the post is still emitted; the lookback walk treats zero-time as in-window).
func TestRowPublishedAtMissing(t *testing.T) {
	if got := rowPublishedAt(`<tr class=""></tr>`); !got.IsZero() {
		t.Fatalf("rowPublishedAt(no date) = %v, want zero time", got)
	}
}

func TestRowTitleUsesTopicHrefAndPrependsTeamTag(t *testing.T) {
	row := `<td class="title">
		<span class="tag"><a href="/topics/list/team_id/816">ANi</a></span>
		<a href="/topics/view/720662_ANi_RentaGirlfriend.html" target="_blank">RentaGirlfriend S05 - 10</a>
	</td>`

	href, title := rowTitle(row)
	if href != "/topics/view/720662_ANi_RentaGirlfriend.html" {
		t.Fatalf("rowTitle href = %q, want topic href", href)
	}
	if title != "[ANi] RentaGirlfriend S05 - 10" {
		t.Fatalf("rowTitle title = %q, want team-prefixed release title", title)
	}
}

func TestRowTitleAlwaysPreservesTeamTag(t *testing.T) {
	row := `<td class="title">
		<span class="tag"><a href="/topics/list/team_id/816">ANi</a></span>
		<a href="/topics/view/720662_ANi_RentaGirlfriend.html" target="_blank">[ANi] RentaGirlfriend S05 - 10</a>
	</td>`

	_, title := rowTitle(row)
	if title != "[ANi] [ANi] RentaGirlfriend S05 - 10" {
		t.Fatalf("rowTitle title = %q, want preserved team prefix", title)
	}
}
