package dmhy

import (
	"context"
	"errors"
	"testing"
	"time"
)

// countingFetcher returns a PageFetcher that tallies upstream calls and a fixed body.
func countingFetcher(calls *int, body []byte, err error) PageFetcher {
	return func(context.Context, int, int) ([]byte, error) {
		*calls++
		return body, err
	}
}

// TestPageCacheDedupsWithinTTL: repeated fetches of the same page within the TTL hit DMHY
// exactly once.
func TestPageCacheDedupsWithinTTL(t *testing.T) {
	var calls int
	c := newPageCache(10 * time.Minute)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	w := c.wrap(countingFetcher(&calls, []byte("page"), nil))

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		b, err := w(ctx, 31, 1)
		if err != nil || string(b) != "page" {
			t.Fatalf("call %d: body=%q err=%v", i, b, err)
		}
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d, want 1 (cached within TTL)", calls)
	}
}

// TestPageCacheKeysDistinct: sort_id and page each form part of the key, so only an
// exact repeat is served from cache.
func TestPageCacheKeysDistinct(t *testing.T) {
	var calls int
	c := newPageCache(time.Minute)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	w := c.wrap(countingFetcher(&calls, []byte("x"), nil))

	ctx := context.Background()
	w(ctx, 31, 1) // different page
	w(ctx, 31, 2) // different page
	w(ctx, 2, 1)  // different sort_id
	w(ctx, 31, 1) // exact repeat -> cached
	if calls != 3 {
		t.Fatalf("upstream calls = %d, want 3 (distinct keys; one repeat cached)", calls)
	}
}

// TestPageCacheExpires: once the TTL elapses, the page is re-fetched.
func TestPageCacheExpires(t *testing.T) {
	var calls int
	c := newPageCache(10 * time.Minute)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	w := c.wrap(countingFetcher(&calls, []byte("x"), nil))

	ctx := context.Background()
	w(ctx, 31, 1) // fetch (calls=1)
	now = now.Add(5 * time.Minute)
	w(ctx, 31, 1)                  // cached (calls=1)
	now = now.Add(6 * time.Minute) // 11m total > 10m TTL
	w(ctx, 31, 1)                  // expired -> refetch (calls=2)
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2 (refetch after TTL)", calls)
	}
}

// TestPageCacheDoesNotCacheErrors: a failed fetch is never cached, so it stays
// immediately retryable.
func TestPageCacheDoesNotCacheErrors(t *testing.T) {
	var calls int
	c := newPageCache(time.Minute)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	w := c.wrap(countingFetcher(&calls, nil, errors.New("boom")))

	ctx := context.Background()
	if _, err := w(ctx, 31, 1); err == nil {
		t.Fatal("want error from failing fetch")
	}
	if _, err := w(ctx, 31, 1); err == nil {
		t.Fatal("want error from failing fetch")
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2 (errors not cached)", calls)
	}
}
