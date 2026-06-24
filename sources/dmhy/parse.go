package dmhy

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseSize parses the human-readable DMHY 大小 size string (e.g. "3.6GB") into an
// integer byte count using the pinned decimal-SI convention (1 GB = 10^9 bytes),
// so the "3.6GB" golden is deterministically 3,600,000,000 (design §8; unit
// convention pinned by the implementation plan). It is a pure-unit linchpin
// independent of any adapter — tests call it directly.
//
// Ported verbatim from internal/source/dmhy/parse.go. The HTML-archive 大小 column
// parse that feeds it real markup is the crawler's job; RSS carries no size.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	// Split the numeric prefix from the unit suffix.
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	numStr := s[:i]
	unit := strings.ToUpper(strings.TrimSpace(s[i:]))
	if numStr == "" {
		return 0, fmt.Errorf("dmhy: parse size %q: no leading number", s)
	}
	n, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("dmhy: parse size %q: %w", s, err)
	}
	var mult float64
	switch unit {
	case "", "B":
		mult = 1
	case "KB":
		mult = 1e3
	case "MB":
		mult = 1e6
	case "GB":
		mult = 1e9
	case "TB":
		mult = 1e12
	default:
		return 0, fmt.Errorf("dmhy: parse size %q: unknown unit %q", s, unit)
	}
	return int64(math.Round(n * mult)), nil
}
