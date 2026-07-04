package nyaa

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestListingPageURL(t *testing.T) {
	got, err := listingPageURL("https://nyaa.si/", "frieren 1080p", "1_2", "2", 3)
	if err != nil {
		t.Fatalf("listingPageURL: %v", err)
	}
	want := "https://nyaa.si/?c=1_2&f=2&p=3&q=frieren+1080p"
	if got != want {
		t.Fatalf("listingPageURL = %q, want %q", got, want)
	}
}

func TestListingPageURLFile(t *testing.T) {
	got, err := listingPageURL("file:///tmp/nyaa", "ignored", "1_2", "2", 3)
	if err != nil {
		t.Fatalf("listingPageURL: %v", err)
	}
	want := "file:///tmp/nyaa/page-3.html"
	if got != want {
		t.Fatalf("listingPageURL = %q, want %q", got, want)
	}
}

func TestFetchPageRateLimiterHonorsCanceledContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page-1.html"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	s := NewServerWithMetricsAndLogger("file://"+dir, "", "", "", 0.01, nil, nil)
	if _, err := s.fetchPage(context.Background(), 1); err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.fetchPage(ctx, 2)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("second fetch error = %v, want context.Canceled", err)
	}
}
