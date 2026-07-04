package nyaa

import (
	"fmt"
	"html"
	"log/slog"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wyvernzora/takuhai/pkg/rawpost"
)

var (
	listTableRe  = regexp.MustCompile(`class="[^"]*\btorrent-list\b[^"]*"`)
	listRowRe    = regexp.MustCompile(`(?s)<tr class="(?:default|success|danger)">`)
	noResultsRe  = regexp.MustCompile(`(?is)<h3\b[^>]*>\s*No results found\s*</h3>`)
	viewAnchorRe = regexp.MustCompile(`(?s)<a([^>]*)href="/view/([0-9]+)"([^>]*)>(.*?)</a>`)
	attrRe       = regexp.MustCompile(`([a-zA-Z0-9_-]+)="([^"]*)"`)
	magnetRe     = regexp.MustCompile(`href="(magnet:[^"]*)"`)
	sizeCellRe   = regexp.MustCompile(`(?s)<td class="text-center">\s*([^<]*?)\s*</td>`)
	timestampRe  = regexp.MustCompile(`data-timestamp="([0-9]+)"`)
	tagRe        = regexp.MustCompile(`<[^>]*>`)
)

// ParseListingPage parses a Nyaa HTML listing page into raw posts.
func ParseListingPage(body []byte) ([]rawpost.RawPost, error) {
	page := string(body)
	starts := listRowRe.FindAllStringIndex(page, -1)
	if len(starts) == 0 {
		if !listTableRe.MatchString(page) && !noResultsRe.MatchString(page) {
			return nil, fmt.Errorf("nyaa: listing page has no torrent-list table")
		}
		return nil, nil
	}

	posts := make([]rawpost.RawPost, 0, len(starts))
	for i, loc := range starts {
		end := len(page)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		row := page[loc[0]:end]
		id, title := rowTitle(row)
		if id == "" {
			continue
		}
		posts = append(posts, rawpost.RawPost{
			Source:      rawpost.SourceNyaa,
			SourceID:    id,
			URL:         "https://nyaa.si/view/" + id,
			Title:       title,
			Magnet:      rowMagnet(row),
			PublishedAt: rowPublishedAt(row),
			SizeBytes:   rowSize(row),
		})
	}
	return posts, nil
}

func rowTitle(row string) (id, title string) {
	m := viewAnchorRe.FindStringSubmatch(row)
	if m == nil {
		return "", ""
	}
	attrs := m[1] + m[3]
	if t := attrValue(attrs, "title"); t != "" {
		title = t
	} else {
		title = strings.TrimSpace(tagRe.ReplaceAllString(m[4], ""))
	}
	return m[2], html.UnescapeString(strings.TrimSpace(title))
}

func attrValue(attrs, name string) string {
	for _, m := range attrRe.FindAllStringSubmatch(attrs, -1) {
		if m[1] == name {
			return html.UnescapeString(m[2])
		}
	}
	return ""
}

func rowMagnet(row string) string {
	if m := magnetRe.FindStringSubmatch(row); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}

func rowSize(row string) int64 {
	if m := sizeCellRe.FindStringSubmatch(row); m != nil {
		size, err := parseSize(m[1])
		if err != nil {
			slog.Warn("parse size failed", "err", err)
			return 0
		}
		return size
	}
	return 0
}

func rowPublishedAt(row string) time.Time {
	m := timestampRe.FindStringSubmatch(row)
	if m == nil {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, fmt.Errorf("nyaa: parse size %q: no leading number", s)
	}
	n, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("nyaa: parse size %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("nyaa: parse size %q: negative size", s)
	}
	unit := "b"
	if len(fields) > 1 {
		unit = strings.ToLower(fields[1])
	}
	mult := float64(1)
	switch unit {
	case "b", "byte", "bytes":
	case "kb":
		mult = 1000
	case "mb":
		mult = 1000 * 1000
	case "gb":
		mult = 1000 * 1000 * 1000
	case "tb":
		mult = 1000 * 1000 * 1000 * 1000
	case "kib":
		mult = 1024
	case "mib":
		mult = 1024 * 1024
	case "gib":
		mult = 1024 * 1024 * 1024
	case "tib":
		mult = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("nyaa: parse size %q: unknown unit %q", s, unit)
	}
	return int64(math.Round(n * mult)), nil
}
