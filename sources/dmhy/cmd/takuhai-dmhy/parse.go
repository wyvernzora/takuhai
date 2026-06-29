package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/wyvernzora/takuhai/pkg/rawpost"
	"github.com/wyvernzora/takuhai/sources/dmhy"
)

// ParseCmd turns locally-saved DMHY archive HTML into RawPost JSONL — one rawpost.RawPost
// per line — using the SAME parser the live crawl uses (dmhy.ParseArchivePage). It is the
// offline backfill on-ramp: save archive pages by hand, parse them here, then POST the
// JSONL into takuhai's /ingest (via n8n or a script). It makes NO network calls and
// derives no infohash — like the live crawler, that is takuhai's job on /ingest.
type ParseCmd struct {
	Files     []string `arg:"" name:"file" help:"DMHY archive HTML file(s); '-' reads stdin."`
	Out       string   `short:"o" name:"out" default:"-" help:"Write the JSONL here ('-' = stdout)."`
	KeepGoing bool     `name:"keep-going" help:"Continue to the next file when one fails to parse, instead of stopping."`
}

// Run parses every input file into RawPost JSONL on --out. A per-file read/parse failure
// stops the run unless --keep-going is set, in which case it is logged and the run still
// exits non-zero if any file failed. A write failure is always fatal.
func (c *ParseCmd) Run() error {
	out := io.Writer(os.Stdout)
	if c.Out != "-" {
		f, err := os.Create(c.Out)
		if err != nil {
			return fmt.Errorf("open output %s: %w", c.Out, err)
		}
		defer f.Close()
		out = f
	}

	posts, failures, err := c.emit(out)
	if err != nil {
		return err
	}
	slog.Info("parse completed",
		"post_count", posts,
		"file_count", len(c.Files),
		"failure_count", failures,
	)
	if failures > 0 {
		return fmt.Errorf("%d of %d file(s) failed to parse", failures, len(c.Files))
	}
	return nil
}

// emit parses each file and JSONL-encodes its posts to w, returning the post and failure
// counts. It returns a non-nil error only on a fatal stop: a write failure, or a per-file
// parse failure when KeepGoing is false.
func (c *ParseCmd) emit(w io.Writer) (posts, failures int, err error) {
	enc := json.NewEncoder(w)
	for _, name := range c.Files {
		parsed, perr := parseFile(name)
		if perr != nil {
			failures++
			if !c.KeepGoing {
				return posts, failures, fmt.Errorf("%s: %w", name, perr)
			}
			slog.Warn("parse file failed", "file", name, "err", perr)
			continue
		}
		for i := range parsed {
			if werr := enc.Encode(&parsed[i]); werr != nil {
				return posts, failures, fmt.Errorf("write JSONL: %w", werr)
			}
			posts++
		}
	}
	return posts, failures, nil
}

// parseFile reads a named file (or stdin for "-") and parses it as a DMHY archive page.
func parseFile(name string) ([]rawpost.RawPost, error) {
	var (
		html []byte
		err  error
	)
	if name == "-" {
		html, err = io.ReadAll(os.Stdin)
	} else {
		html, err = os.ReadFile(name)
	}
	if err != nil {
		return nil, err
	}
	return dmhy.ParseArchivePage(html)
}
