package nyaa

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestCrawlPaginatesWithCursor(t *testing.T) {
	fetch := func(_ context.Context, page int) ([]byte, error) {
		switch page {
		case 1:
			return []byte(listingWithItems(1, 2)), nil
		case 2:
			return []byte(listingWithItems(3)), nil
		default:
			return []byte(emptyListingPage), nil
		}
	}
	c := NewCrawler(fetch, Threshold)

	first, err := c.Crawl(context.Background(), CrawlRequest{PageSize: 1}, 0)
	if err != nil {
		t.Fatalf("first Crawl: %v", err)
	}
	if len(first.Posts) != 1 || first.NextCursor != "1:1" || !first.HasMore {
		t.Fatalf("first response = %+v, want one post and cursor 1:1", first)
	}

	second, err := c.Crawl(context.Background(), CrawlRequest{PageSize: 2, Cursor: first.NextCursor}, 0)
	if err != nil {
		t.Fatalf("second Crawl: %v", err)
	}
	if len(second.Posts) != 2 || second.NextCursor != "2:0" || !second.HasMore {
		t.Fatalf("second response = %+v, want two posts and cursor 2:0", second)
	}
}

func TestCrawlStopsAtLookbackBoundary(t *testing.T) {
	fetch := func(_ context.Context, page int) ([]byte, error) {
		if page != 1 {
			return []byte(emptyListingPage), nil
		}
		return []byte(listingWithTimes(
			time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC),
			time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		)), nil
	}
	c := NewCrawler(fetch, Threshold)
	c.now = func() time.Time { return time.Date(2026, 7, 4, 13, 0, 0, 0, time.UTC) }

	resp, err := c.Crawl(context.Background(), CrawlRequest{PageSize: 10}, 48*time.Hour)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if len(resp.Posts) != 1 || resp.HasMore || resp.NextCursor != "" {
		t.Fatalf("response = %+v, want one in-window post and no cursor", resp)
	}
}

func TestCrawlRequiresConsecutiveEmptyPagesForFeedFloor(t *testing.T) {
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
	if len(resp.Posts) != 1 || resp.Posts[0].SourceID != "200" {
		t.Fatalf("posts = %+v, want valid post after transient empty page", resp.Posts)
	}
	if resp.HasMore || resp.NextCursor != "" || resp.stopReason != "feed_floor" {
		t.Fatalf("response = %+v, want feed_floor with no cursor", resp)
	}
	wantFetched := []int{1, 2, 3, 4}
	if fmt.Sprint(fetched) != fmt.Sprint(wantFetched) {
		t.Fatalf("fetched pages = %v, want %v", fetched, wantFetched)
	}
}

func listingWithItems(ids ...int) string {
	times := make([]time.Time, 0, len(ids))
	for range ids {
		times = append(times, time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC))
	}
	return listingWith(ids, times)
}

func listingWithTimes(times ...time.Time) string {
	ids := make([]int, 0, len(times))
	for i := range times {
		ids = append(ids, i+1)
	}
	return listingWith(ids, times)
}

func listingWith(ids []int, times []time.Time) string {
	var rows string
	for i, id := range ids {
		rows += fmt.Sprintf(`<tr class="default">
<td><a href="/?c=1_2" title="Anime - English-translated"></a></td>
<td colspan="2"><a href="/view/%d" title="item %d">item %d</a></td>
<td class="text-center"><a href="/download/%d.torrent"></a><a href="magnet:?xt=urn:btih:%040d"></a></td>
<td class="text-center">1 MiB</td>
<td class="text-center" data-timestamp="%d">%s</td>
<td class="text-center">1</td><td class="text-center">0</td><td class="text-center">3</td>
</tr>`, id, id, id, id, id, times[i].Unix(), times[i].Format("2006-01-02 15:04"))
	}
	return `<table class="table torrent-list table-bordered"><tbody>` + rows + `</tbody></table>`
}
