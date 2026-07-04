//go:build conformance

// Nyaa crawler conformance suite for the stateless POST /crawl contract.
//
// Fixture provenance: testdata/live-listing-p2.html is a real Nyaa listing page from
// https://nyaa.si/?c=1_0&f=0&p=2 fetched on 2026-07-04; testdata/live-no-results.html
// is a real empty-results page from nyaa.si fetched the same day. Do not trim them.

package nyaa

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
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wyvernzora/takuhai/pkg/crawl"
	"github.com/wyvernzora/takuhai/pkg/rawpost"
)

const (
	liveListingFixture   = "live-listing-p2.html"
	liveNoResultsFixture = "live-no-results.html"
)

var errNyaaTransient = errors.New("nyaa-conformance: injected transient fetch failure")

func loadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func crawlServer(t *testing.T, fetch PageFetcher) *httptest.Server {
	t.Helper()
	c := NewCrawler(fetch, Threshold)
	s := &Server{
		crawler: c,
		handler: crawl.NewServer(crawl.ServerConfig{
			Source:  "nyaa",
			Crawler: c.shared(),
		}),
	}
	mux := http.NewServeMux()
	mux.Handle("/crawl", s)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != want {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, want, raw)
	}
}

func TestP4_NyaaCrawlHTTP_WireContract(t *testing.T) {
	t.Run("method-routing-get-is-405", func(t *testing.T) {
		ts := crawlServer(t, func(context.Context, int) ([]byte, error) {
			t.Fatal("fetcher reached on GET")
			return nil, nil
		})
		resp, err := http.Get(ts.URL + "/crawl")
		if err != nil {
			t.Fatalf("GET /crawl: %v", err)
		}
		assertStatus(t, resp, http.StatusMethodNotAllowed)
	})

	t.Run("malformed-body-is-400", func(t *testing.T) {
		ts := crawlServer(t, func(context.Context, int) ([]byte, error) {
			t.Fatal("fetcher reached on malformed body")
			return nil, nil
		})
		resp, err := http.Post(ts.URL+"/crawl", "application/json", strings.NewReader("{bad json"))
		if err != nil {
			t.Fatalf("POST /crawl: %v", err)
		}
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("bad-cursor-is-400", func(t *testing.T) {
		ts := crawlServer(t, func(context.Context, int) ([]byte, error) {
			t.Fatal("fetcher reached on bad cursor")
			return nil, nil
		})
		resp := postJSON(t, ts.URL+"/crawl", CrawlRequest{Cursor: "not-a-cursor"})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("bad-lookback-is-400", func(t *testing.T) {
		ts := crawlServer(t, func(context.Context, int) ([]byte, error) {
			t.Fatal("fetcher reached on bad lookback")
			return nil, nil
		})
		resp := postJSON(t, ts.URL+"/crawl", CrawlRequest{Lookback: "12 parsecs"})
		assertStatus(t, resp, http.StatusBadRequest)
	})

	t.Run("fetch-error-is-502", func(t *testing.T) {
		ts := crawlServer(t, func(context.Context, int) ([]byte, error) {
			return nil, errNyaaTransient
		})
		resp := postJSON(t, ts.URL+"/crawl", CrawlRequest{PageSize: 10})
		assertStatus(t, resp, http.StatusBadGateway)
	})

	t.Run("parse-error-is-502", func(t *testing.T) {
		ts := crawlServer(t, func(context.Context, int) ([]byte, error) {
			return []byte(`<!doctype html><html><body><h1>not a listing</h1></body></html>`), nil
		})
		resp := postJSON(t, ts.URL+"/crawl", CrawlRequest{PageSize: 10})
		assertStatus(t, resp, http.StatusBadGateway)
	})

	t.Run("success-envelope-shape", func(t *testing.T) {
		ts := crawlServer(t, func(context.Context, int) ([]byte, error) {
			return []byte(listingWithItems(1, 2)), nil
		})
		resp := postJSON(t, ts.URL+"/crawl", CrawlRequest{PageSize: 1})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, raw)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			t.Fatalf("decode response object: %v", err)
		}
		for _, key := range []string{"posts", "next_cursor", "has_more"} {
			if _, ok := raw[key]; !ok {
				t.Fatalf("response missing key %q: %v", key, raw)
			}
		}
		var got CrawlResponse
		if err := json.Unmarshal(mustMarshal(t, raw), &got); err != nil {
			t.Fatalf("decode typed response: %v", err)
		}
		if len(got.Posts) != 1 || got.Posts[0].Source != rawpost.SourceNyaa {
			t.Fatalf("posts = %+v, want one nyaa post", got.Posts)
		}
		if got.NextCursor == "" || !got.HasMore {
			t.Fatalf("next_cursor=%q has_more=%v, want continuing cursor", got.NextCursor, got.HasMore)
		}
	})
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestP4_NyaaParse_LiveListingGolden(t *testing.T) {
	posts, err := ParseListingPage(loadTestdata(t, liveListingFixture))
	if err != nil {
		t.Fatalf("ParseListingPage(%s): %v", liveListingFixture, err)
	}
	if len(posts) != 75 {
		t.Fatalf("len(posts) = %d, want 75", len(posts))
	}

	first := posts[0]
	if first.Source != rawpost.SourceNyaa {
		t.Fatalf("Source = %q, want %q", first.Source, rawpost.SourceNyaa)
	}
	if first.SourceID != "2128319" {
		t.Fatalf("SourceID = %q, want 2128319", first.SourceID)
	}
	if _, err := strconv.Atoi(first.SourceID); err != nil {
		t.Fatalf("SourceID = %q, want numeric: %v", first.SourceID, err)
	}
	if first.URL != "https://nyaa.si/view/2128319" {
		t.Fatalf("URL = %q, want https://nyaa.si/view/2128319", first.URL)
	}
	wantTitle := "[AsukaRaws] Mahou Shoujo Lyrical Nanoha EXGV - 01 (WEB-DL 1280x720 x264 AAC)"
	if first.Title != wantTitle {
		t.Fatalf("Title = %q, want %q", first.Title, wantTitle)
	}
	if !strings.HasPrefix(first.Magnet, "magnet:?xt=urn:btih:") {
		t.Fatalf("Magnet = %q, want btih magnet prefix", first.Magnet)
	}
	if first.SizeBytes != 584685978 {
		t.Fatalf("SizeBytes = %d, want 584685978", first.SizeBytes)
	}
	wantTime := time.Unix(1783185005, 0).UTC()
	if !first.PublishedAt.Equal(wantTime) {
		t.Fatalf("PublishedAt = %s, want %s", first.PublishedAt, wantTime)
	}
	if first.PublishedAt.Location() != time.UTC {
		t.Fatalf("PublishedAt location = %v, want UTC", first.PublishedAt.Location())
	}
}

func TestP4_NyaaParse_LiveNoResultsIsEmpty(t *testing.T) {
	posts, err := ParseListingPage(loadTestdata(t, liveNoResultsFixture))
	if err != nil {
		t.Fatalf("ParseListingPage(%s): %v", liveNoResultsFixture, err)
	}
	if len(posts) != 0 {
		t.Fatalf("len(posts) = %d, want 0", len(posts))
	}
}

func TestP4_NyaaCrawl_FloorRequiresConsecutiveEmptyPages(t *testing.T) {
	var fetched []int
	fetch := func(_ context.Context, page int) ([]byte, error) {
		fetched = append(fetched, page)
		switch page {
		case 1, 3, 4:
			return []byte(noResultsListingPage), nil
		case 2:
			return []byte(listingWithItems(200)), nil
		default:
			return nil, fmt.Errorf("unexpected page %d", page)
		}
	}
	c := NewCrawler(fetch, Threshold)

	resp, err := c.Crawl(context.Background(), CrawlRequest{PageSize: 10}, 0)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if got := postIDs(resp.Posts); !reflect.DeepEqual(got, []string{"200"}) {
		t.Fatalf("post IDs = %v, want [200]", got)
	}
	if resp.HasMore || resp.NextCursor != "" {
		t.Fatalf("has_more=%v next_cursor=%q, want confirmed floor", resp.HasMore, resp.NextCursor)
	}
	if !reflect.DeepEqual(fetched, []int{1, 2, 3, 4}) {
		t.Fatalf("fetched = %v, want [1 2 3 4]", fetched)
	}
}

func TestP4_NyaaCrawl_CursorWalkAndLookbackBoundary(t *testing.T) {
	t.Run("cursor-walk-no-duplicates-or-skips", func(t *testing.T) {
		// synthetic pages: four rows across two result pages, then the real no-results shape.
		fetch := func(_ context.Context, page int) ([]byte, error) {
			switch page {
			case 1:
				return []byte(listingWithItems(1, 2)), nil
			case 2:
				return []byte(listingWithItems(3, 4)), nil
			default:
				return []byte(noResultsListingPage), nil
			}
		}
		c := NewCrawler(fetch, Threshold)

		var (
			cursor string
			ids    []string
		)
		for i := 0; i < 4; i++ {
			resp, err := c.Crawl(context.Background(), CrawlRequest{PageSize: 1, Cursor: cursor}, 0)
			if err != nil {
				t.Fatalf("Crawl step %d: %v", i+1, err)
			}
			if len(resp.Posts) != 1 {
				t.Fatalf("step %d posts = %d, want 1", i+1, len(resp.Posts))
			}
			if !resp.HasMore || resp.NextCursor == "" {
				t.Fatalf("step %d has_more=%v cursor=%q, want continuing cursor", i+1, resp.HasMore, resp.NextCursor)
			}
			if _, _, err := parseCursor(resp.NextCursor); err != nil {
				t.Fatalf("step %d next_cursor %q does not round-trip: %v", i+1, resp.NextCursor, err)
			}
			ids = append(ids, resp.Posts[0].SourceID)
			cursor = resp.NextCursor
		}
		if !reflect.DeepEqual(ids, []string{"1", "2", "3", "4"}) {
			t.Fatalf("walked IDs = %v, want [1 2 3 4]", ids)
		}
	})

	t.Run("lookback-boundary-stops", func(t *testing.T) {
		newer := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
		older := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
		fetch := func(_ context.Context, page int) ([]byte, error) {
			if page != 1 {
				return []byte(noResultsListingPage), nil
			}
			return []byte(listingWithTimes(newer, newer, older)), nil
		}
		c := NewCrawler(fetch, Threshold)
		c.now = func() time.Time { return time.Date(2026, 7, 4, 13, 0, 0, 0, time.UTC) }

		resp, err := c.Crawl(context.Background(), CrawlRequest{PageSize: 3}, 48*time.Hour)
		if err != nil {
			t.Fatalf("Crawl: %v", err)
		}
		if got := postIDs(resp.Posts); !reflect.DeepEqual(got, []string{"1", "2"}) {
			t.Fatalf("post IDs = %v, want [1 2]", got)
		}
		if resp.HasMore || resp.NextCursor != "" {
			t.Fatalf("has_more=%v next_cursor=%q, want lookback stop", resp.HasMore, resp.NextCursor)
		}
	})
}

func TestP4_NyaaCrawlHTTP_TransientFetchErrorMidWalkIs502(t *testing.T) {
	ts := crawlServer(t, func(_ context.Context, page int) ([]byte, error) {
		switch page {
		case 1:
			return []byte(noResultsListingPage), nil
		case 2:
			return nil, errNyaaTransient
		default:
			return nil, fmt.Errorf("unexpected page %d", page)
		}
	})
	resp := postJSON(t, ts.URL+"/crawl", CrawlRequest{PageSize: 10})
	assertStatus(t, resp, http.StatusBadGateway)
}

func TestP4_NyaaParse_BadSizeKeepsPostWithZeroBytes(t *testing.T) {
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
}

func postIDs(posts []rawpost.RawPost) []string {
	ids := make([]string, len(posts))
	for i, p := range posts {
		ids[i] = p.SourceID
	}
	return ids
}
