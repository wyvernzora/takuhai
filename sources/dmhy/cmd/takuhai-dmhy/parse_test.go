package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wyvernzora/takuhai/pkg/rawpost"
	"github.com/wyvernzora/takuhai/sources/dmhy"
)

// fixtureDir holds the same DMHY archive fixtures the conformance suite drives, reached
// from this command package's working directory (the package dir at test time).
const fixtureDir = "../../testdata/html"

// decodeJSONL reads consecutive JSON objects (one per line) into RawPosts.
func decodeJSONL(t *testing.T, r *bytes.Buffer) []rawpost.RawPost {
	t.Helper()
	dec := json.NewDecoder(r)
	var out []rawpost.RawPost
	for dec.More() {
		var p rawpost.RawPost
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decode JSONL: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// TestParseCmdEmitsJSONL: a real content page emits exactly one JSONL line per parsed
// post, each a well-formed RawPost stamped with the source.
func TestParseCmdEmitsJSONL(t *testing.T) {
	page := filepath.Join(fixtureDir, "page-real.html")
	html, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	want, err := dmhy.ParseArchivePage(html)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("fixture page-real.html parsed to zero posts; test needs a content page")
	}

	var buf bytes.Buffer
	cmd := &ParseCmd{Files: []string{page}, Out: "-"}
	posts, failures, err := cmd.emit(&buf)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if failures != 0 || posts != len(want) {
		t.Fatalf("emit = (%d posts, %d failures), want (%d, 0)", posts, failures, len(want))
	}

	got := decodeJSONL(t, &buf)
	if len(got) != len(want) {
		t.Fatalf("decoded %d JSONL lines, want %d", len(got), len(want))
	}
	for i, p := range got {
		if p.Source != rawpost.SourceDMHY {
			t.Errorf("post %d Source = %q, want %q", i, p.Source, rawpost.SourceDMHY)
		}
		if p.Title == "" {
			t.Errorf("post %d has empty Title", i)
		}
		if p.SizeBytes <= 0 {
			t.Errorf("post %d has SizeBytes=%d, want > 0", i, p.SizeBytes)
		}
	}
}

// TestParseCmdNonArchiveErrors: a non-archive 200 page surfaces as a parse error (the
// same guard the live walk relies on), never a silent zero-post success.
func TestParseCmdNonArchiveErrors(t *testing.T) {
	page := filepath.Join(fixtureDir, "non-archive-200.html")
	var buf bytes.Buffer
	cmd := &ParseCmd{Files: []string{page}, Out: "-"}
	if _, _, err := cmd.emit(&buf); err == nil {
		t.Fatal("emit(non-archive-200.html) returned nil error; a non-archive page must fail")
	}
}

// TestParseCmdKeepGoing: with --keep-going a bad file is counted and skipped, and the
// good file's posts are still emitted.
func TestParseCmdKeepGoing(t *testing.T) {
	bad := filepath.Join(fixtureDir, "non-archive-200.html")
	good := filepath.Join(fixtureDir, "page-real.html")
	var buf bytes.Buffer
	cmd := &ParseCmd{Files: []string{bad, good}, Out: "-", KeepGoing: true}
	posts, failures, err := cmd.emit(&buf)
	if err != nil {
		t.Fatalf("emit with keep-going returned a fatal error: %v", err)
	}
	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}
	if posts == 0 {
		t.Fatal("keep-going should still emit the good file's posts")
	}
}
