package dmhy

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// extDurationPrefixRe strips a leading <int>w and/or <int>d (weeks/days) prefix off an
// extended duration string. time.ParseDuration tops out at hours, so days/weeks are
// handled here and the remainder delegated to the standard parser.
var extDurationPrefixRe = regexp.MustCompile(`^(?:(\d+)w)?(?:(\d+)d)?`)

// parseLookback parses an extended Go duration: an optional leading <int>w and/or
// <int>d (weeks/days) prefix is stripped and added on, the remainder delegated to
// time.ParseDuration (so 30d, 2w, 36h, 90m, 2w12h all parse). "" or a result of 0
// means "no lookback limit" (0). A malformed input (negatives, trailing garbage,
// anything time.ParseDuration rejects) is a hard error — the caller maps it to a 400
// client error, never a 502.
func parseLookback(s string) (time.Duration, error) {
	if s == "" || s == "0" {
		return 0, nil
	}

	m := extDurationPrefixRe.FindStringSubmatch(s)
	// m[0] is the matched prefix (possibly empty); m[1]=weeks, m[2]=days.
	var ext time.Duration
	if m[1] != "" {
		w, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, fmt.Errorf("dmhy: malformed lookback %q: %w", s, err)
		}
		ext += time.Duration(w) * 7 * 24 * time.Hour
	}
	if m[2] != "" {
		d, err := strconv.Atoi(m[2])
		if err != nil {
			return 0, fmt.Errorf("dmhy: malformed lookback %q: %w", s, err)
		}
		ext += time.Duration(d) * 24 * time.Hour
	}

	rest := s[len(m[0]):]
	if rest != "" {
		d, err := time.ParseDuration(rest)
		if err != nil {
			return 0, fmt.Errorf("dmhy: malformed lookback %q: %w", s, err)
		}
		if d < 0 {
			return 0, fmt.Errorf("dmhy: malformed lookback %q: negative duration", s)
		}
		ext += d
	}

	return ext, nil
}
