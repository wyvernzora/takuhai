package dmhy

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// pageCache is a small TTL cache over a PageFetcher: within ttl, a given
// (sortID, page) is fetched from DMHY at most once. An n8n catch-up loop that
// re-walks the same pages every poll (the A-style "stop when /ingest reports no new
// posts" pattern) therefore stops re-hitting DMHY for content it just fetched.
//
// It caches EVERY page including the newest — within ttl a page's bytes are frozen, so
// brand-new posts on the frontier are not seen until the entry expires. Keep ttl at or
// below the poll interval if frontier freshness matters.
//
// Only successful 200-fetches are cached; a fetch error is never cached, so a transient
// upstream blip stays immediately retryable (the §1/§5/§8 "a failure is never an empty
// page" contract is preserved — the cache only ever returns bytes a real fetch returned).
//
// ponytail: a plain mutex-guarded map with sweep-on-insert. The entry count is bounded
// by the crawler's rate limit × ttl (the limiter caps fetches/sec, the sweep drops
// expired entries), so no LRU/size cap is needed at this scale.
type pageCache struct {
	ttl   time.Duration
	now   func() time.Time // injectable clock; real builds use time.Now
	mu    sync.Mutex
	items map[string]pageEntry
}

type pageEntry struct {
	body    []byte
	expires time.Time
}

func newPageCache(ttl time.Duration) *pageCache {
	return &pageCache{ttl: ttl, now: time.Now, items: make(map[string]pageEntry)}
}

func pageCacheKey(sortID, page int) string {
	return strconv.Itoa(sortID) + "|" + strconv.Itoa(page)
}

// wrap returns a PageFetcher that serves fresh cache hits and otherwise delegates to
// fetch, caching successful results under the (sortID, page) key.
func (c *pageCache) wrap(fetch PageFetcher) PageFetcher {
	return func(ctx context.Context, sortID, page int) ([]byte, error) {
		key := pageCacheKey(sortID, page)
		if body, ok := c.get(key); ok {
			return body, nil
		}
		body, err := fetch(ctx, sortID, page)
		if err != nil {
			return nil, err // never cache a failure
		}
		c.put(key, body)
		return body, nil
	}
}

func (c *pageCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || !c.now().Before(e.expires) {
		return nil, false
	}
	return e.body, true
}

func (c *pageCache) put(key string, body []byte) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	// Sweep expired entries so the map stays bounded to pages fetched within the ttl.
	for k, e := range c.items {
		if !now.Before(e.expires) {
			delete(c.items, k)
		}
	}
	c.items[key] = pageEntry{body: body, expires: now.Add(c.ttl)}
}
