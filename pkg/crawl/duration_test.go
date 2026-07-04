package crawl

import (
	"testing"
	"time"
)

func TestParseLookback(t *testing.T) {
	const (
		hour = time.Hour
		day  = 24 * time.Hour
		week = 7 * day
	)

	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		// "" / "0" = no limit.
		{in: "", want: 0},
		{in: "0", want: 0},
		// Standard time.ParseDuration units still work.
		{in: "36h", want: 36 * hour},
		{in: "90m", want: 90 * time.Minute},
		{in: "12h", want: 12 * hour},
		// Extended day/week units (time.ParseDuration cannot parse these).
		{in: "30d", want: 30 * day},
		{in: "2w", want: 2 * week},
		{in: "1d", want: day},
		// Combinations.
		{in: "2w12h", want: 2*week + 12*hour},
		{in: "1w3d", want: week + 3*day},
		{in: "2w3d4h", want: 2*week + 3*day + 4*hour},
		// Malformed.
		{in: "abc", wantErr: true},
		{in: "-5d", wantErr: true},
		{in: "5x", wantErr: true},
		{in: "30days", wantErr: true},
		{in: "d", wantErr: true},
		{in: "12h-", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseLookback("dmhy", tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseLookback(%q) = %v, nil; want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLookback(%q): unexpected error %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseLookback(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
