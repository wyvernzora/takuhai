// Package infohash owns the canonical dedup key. takuhai derives the
// canonical v1 btih (40 lowercase hex) from each post's magnet on /ingest and
// drops pure-v2/malformed values; the crawler stays dumb and emits raw posts
// with no infohash. This package is a leaf — it imports nothing internal.
package infohash

import (
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrSkipInfohash is the REAL contract sentinel NormalizeInfohash returns for an
// in-scope skip: an empty, pure-v2, or non-40-hex infohash that ingest must drop
// (never insert, never abort the page — design §5 step 0). The conformance
// pure-v2/malformed-skip test asserts this specific sentinel.
var ErrSkipInfohash = errors.New("takuhai/infohash: infohash skipped (empty, pure-v2, or non-40-hex)")

// NormalizeInfohash resolves an inbound infohash (or magnet xt payload) to the
// canonical v1 btih: 40 lowercase hex (design §2/§3). A 32-char RFC 4648 base32
// btih is decoded to hex; a hybrid carries both urns and keys on its v1 btih; a
// pure-v2 (urn:btmh-only) or malformed value is reported as a skip via
// ErrSkipInfohash. This is the dedup linchpin: the same torrent must always
// normalize to the same 40-hex string (design §3 "Infohash normalization").
//
// The base32->hex decode is the new capability layered on the dmhy-mcp parseInfoHash
// (whose 32-char branch does NOT decode base32).
func NormalizeInfohash(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrSkipInfohash
	}
	// Magnet/urn payload: take the v1 btih, ignore any v2 btmh. A payload carrying a
	// urn but no urn:btih (e.g. a pure-v2 urn:btmh-only magnet) has no canonical v1
	// identity and is skipped (design §2/§3/§5 step 0).
	if i := strings.Index(s, "urn:btih:"); i >= 0 {
		s = s[i+len("urn:btih:"):]
		if j := strings.IndexByte(s, '&'); j >= 0 {
			s = s[:j]
		}
	} else if strings.HasPrefix(s, "magnet:") || strings.Contains(s, "urn:btmh:") {
		return "", ErrSkipInfohash
	}
	return canonicalBtih(s)
}

// canonicalBtih normalizes a bare btih (40-hex or 32-char RFC4648 base32) to the
// canonical 40 lowercase hex, or reports ErrSkipInfohash if it is neither.
func canonicalBtih(s string) (string, error) {
	switch len(s) {
	case 40:
		if isHex40(s) {
			return strings.ToLower(s), nil
		}
	case 32:
		if b, err := base32.StdEncoding.DecodeString(strings.ToUpper(s)); err == nil && len(b) == 20 {
			return hex.EncodeToString(b), nil
		}
	}
	return "", ErrSkipInfohash
}

func isHex40(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
