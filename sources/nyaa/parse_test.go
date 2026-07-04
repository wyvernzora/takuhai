package nyaa

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wyvernzora/takuhai/pkg/rawpost"
)

func TestParseListingPage(t *testing.T) {
	posts, err := ParseListingPage([]byte(sampleListingPage))
	if err != nil {
		t.Fatalf("ParseListingPage: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("len(posts) = %d, want 1", len(posts))
	}
	got := posts[0]
	if got.Source != rawpost.SourceNyaa {
		t.Fatalf("Source = %q, want %q", got.Source, rawpost.SourceNyaa)
	}
	if got.SourceID != "1234567" {
		t.Fatalf("SourceID = %q, want 1234567", got.SourceID)
	}
	if got.URL != "https://nyaa.si/view/1234567" {
		t.Fatalf("URL = %q", got.URL)
	}
	if got.Title != "[SubsPlease] Example - 01 (1080p) [ABCDEF12].mkv" {
		t.Fatalf("Title = %q", got.Title)
	}
	if want := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=udp%3A%2F%2Ftracker"; got.Magnet != want {
		t.Fatalf("Magnet = %q, want %q", got.Magnet, want)
	}
	if got.SizeBytes != 1503238554 {
		t.Fatalf("SizeBytes = %d, want 1503238554", got.SizeBytes)
	}
	wantTime := time.Date(2026, 7, 4, 18, 30, 0, 0, time.UTC)
	if !got.PublishedAt.Equal(wantTime) {
		t.Fatalf("PublishedAt = %s, want %s", got.PublishedAt, wantTime)
	}
}

func TestParseListingPageEmpty(t *testing.T) {
	posts, err := ParseListingPage([]byte(emptyListingPage))
	if err != nil {
		t.Fatalf("ParseListingPage: %v", err)
	}
	if len(posts) != 0 {
		t.Fatalf("len(posts) = %d, want 0", len(posts))
	}
}

func TestParseListingPageNoResultsMarkerIsEmpty(t *testing.T) {
	posts, err := ParseListingPage([]byte(noResultsListingPage))
	if err != nil {
		t.Fatalf("ParseListingPage: %v", err)
	}
	if posts != nil {
		t.Fatalf("posts = %#v, want nil", posts)
	}
}

func TestParseListingPageWithoutTableOrNoResultsMarkerIsError(t *testing.T) {
	_, err := ParseListingPage([]byte(`<!DOCTYPE html><html><body><h1>Checking your browser</h1></body></html>`))
	if err == nil {
		t.Fatal("ParseListingPage error = nil, want error")
	}
}

func TestParseListingPageSkipsMalformedRows(t *testing.T) {
	body := `<!DOCTYPE html>
<html><body>
<table class="table torrent-list table-bordered table-hover table-striped">
<tbody>
<tr class="default">
  <td><a href="/?c=1_2" title="Anime - English-translated"></a></td>
  <td colspan="2"><a href="/view/111" title="valid one">valid one</a></td>
  <td class="text-center"><a href="magnet:?xt=urn:btih:0000000000000000000000000000000000000111"></a></td>
  <td class="text-center">1 MiB</td>
  <td class="text-center" data-timestamp="1783189800">2026-07-04 18:30</td>
  <td class="text-center">1</td><td class="text-center">0</td><td class="text-center">3</td>
</tr>
<tr class="default">
  <td><a href="/?c=1_2" title="Anime - English-translated"></a></td>
  <td colspan="2"><a href="/broken" title="malformed">malformed</a></td>
  <td class="text-center"><a href="magnet:?xt=urn:btih:0000000000000000000000000000000000000222"></a></td>
  <td class="text-center">2 MiB</td>
  <td class="text-center" data-timestamp="1783189800">2026-07-04 18:30</td>
  <td class="text-center">1</td><td class="text-center">0</td><td class="text-center">3</td>
</tr>
<tr class="default">
  <td><a href="/?c=1_2" title="Anime - English-translated"></a></td>
  <td colspan="2"><a href="/view/333" title="valid two">valid two</a></td>
  <td class="text-center"><a href="magnet:?xt=urn:btih:0000000000000000000000000000000000000333"></a></td>
  <td class="text-center">3 MiB</td>
  <td class="text-center" data-timestamp="1783189800">2026-07-04 18:30</td>
  <td class="text-center">1</td><td class="text-center">0</td><td class="text-center">3</td>
</tr>
</tbody>
</table>
</body></html>`

	posts, err := ParseListingPage([]byte(body))
	if err != nil {
		t.Fatalf("ParseListingPage: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("len(posts) = %d, want 2", len(posts))
	}
	for _, p := range posts {
		if p.SourceID == "" || p.URL == "https://nyaa.si/view/" {
			t.Fatalf("malformed output post: %+v", p)
		}
	}
	if posts[0].SourceID != "111" || posts[1].SourceID != "333" {
		t.Fatalf("source IDs = %q, %q; want 111, 333", posts[0].SourceID, posts[1].SourceID)
	}
}

func TestParseSizeRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "unknown unit", in: "1 ZiB"},
		{name: "garbage number", in: "garbage MiB"},
		{name: "empty", in: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSize(tt.in)
			if err == nil {
				t.Fatalf("parseSize(%q) error = nil, want error", tt.in)
			}
			if got != 0 {
				t.Fatalf("parseSize(%q) = %d, want 0 on error", tt.in, got)
			}
		})
	}
}

func TestParseListingPageBadSizeLogsAndKeepsRow(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)

	body := strings.Replace(sampleListingPage, "1.4 GiB", "1 ZiB", 1)
	posts, err := ParseListingPage([]byte(body))
	if err != nil {
		t.Fatalf("ParseListingPage: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("len(posts) = %d, want 1", len(posts))
	}
	if posts[0].SizeBytes != 0 {
		t.Fatalf("SizeBytes = %d, want 0", posts[0].SizeBytes)
	}
	log := logs.String()
	if !strings.Contains(log, "parse size failed") || !strings.Contains(log, "unknown unit") {
		t.Fatalf("log = %q, want parse size failure with unknown unit", log)
	}
}

// Trimmed from the nyaa.si listing shape at https://nyaa.si/?c=1_0&f=0&p=1:
// torrent-list table, row class, /view link, magnet anchor, size cell, and
// machine-readable data-timestamp cell.
const sampleListingPage = `<!DOCTYPE html>
<html>
<body>
<table class="table torrent-list table-bordered table-hover table-striped">
<tbody>
<tr class="default">
  <td><a href="/?c=1_2" title="Anime - English-translated"><img src="/static/img/icons/nyaa/1_2.png" alt="Anime - English-translated" class="category-icon"></a></td>
  <td colspan="2"><a href="/view/1234567" title="[SubsPlease] Example - 01 (1080p) [ABCDEF12].mkv">[SubsPlease] Example - 01 (1080p) [ABCDEF12].mkv</a></td>
  <td class="text-center"><a href="/download/1234567.torrent"><i class="fa fa-fw fa-download"></i></a><a href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&amp;tr=udp%3A%2F%2Ftracker"><i class="fa fa-fw fa-magnet"></i></a></td>
  <td class="text-center">1.4 GiB</td>
  <td class="text-center" data-timestamp="1783189800">2026-07-04 18:30</td>
  <td class="text-center">1</td>
  <td class="text-center">0</td>
  <td class="text-center">3</td>
</tr>
</tbody>
</table>
</body>
</html>`

const emptyListingPage = `<!DOCTYPE html>
<html><body>
<table class="table torrent-list table-bordered table-hover table-striped">
<tbody></tbody>
</table>
</body></html>`

const noResultsListingPage = `<!DOCTYPE html>
<html lang="en">
<head><title>Nyaa - Browse</title></head>
<body>
<nav class="navbar navbar-default navbar-static-top">
  <div class="container"><a class="navbar-brand" href="/">Nyaa</a></div>
</nav>
<div class="container">
  <form class="navbar-form" action="/" method="get">
    <input class="form-control" name="q" value="missing-release">
    <button class="btn btn-primary" type="submit">Search</button>
  </form>
  <div class="row">
    <div class="col-md-12">
      <h3 class="text-center">
        No results found
      </h3>
    </div>
  </div>
</div>
</body>
</html>`
