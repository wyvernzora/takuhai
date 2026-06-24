package infohash

import (
	"errors"
	"strings"
	"testing"
)

// canonicalHex is the canonical v1 btih the base32 vector JN7DUBILGVIFL46NCSOF42GZ5JCXNNUS
// decodes to (cross-checked against the conformance goldens). The two must stay in sync:
// the dedup linchpin is that the same torrent always normalizes to this exact 40-hex string.
const (
	canonicalHex = "4b7e3a050b355055f3cd149c5e68d9ea4576b692"
	base32Btih   = "JN7DUBILGVIFL46NCSOF42GZ5JCXNNUS"
)

// TestNormalizeInfohash_OK is the fast, hermetic (no build tag, no Postgres) regression
// net for the dedup-key canonicalization: every accepting branch of NormalizeInfohash
// must land on the canonical 40 lowercase hex.
func TestNormalizeInfohash_OK(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare-40-hex-lowercase", canonicalHex, canonicalHex},
		{"bare-40-hex-uppercase-lowercased", strings.ToUpper(canonicalHex), canonicalHex},
		{"bare-40-hex-mixedcase-lowercased", "4B7e3a050B355055f3CD149c5e68D9ea4576B692", canonicalHex},
		{"bare-40-hex-surrounding-whitespace-trimmed", "  " + canonicalHex + "\n", canonicalHex},
		{"bare-base32-decoded-to-hex", base32Btih, canonicalHex},
		{"bare-base32-lowercase-decoded-to-hex", strings.ToLower(base32Btih), canonicalHex},
		{"magnet-btih-hex", "magnet:?xt=urn:btih:" + canonicalHex, canonicalHex},
		{"magnet-btih-base32", "magnet:?xt=urn:btih:" + base32Btih, canonicalHex},
		{
			"magnet-btih-with-trailing-params",
			"magnet:?xt=urn:btih:" + canonicalHex + "&dn=Some.Release&tr=udp://x",
			canonicalHex,
		},
		{
			"hybrid-magnet-keys-on-v1-btih",
			"magnet:?xt=urn:btih:" + canonicalHex +
				"&xt=urn:btmh:1220caf1d2e3b4a5968778695a4b3c2d1e0f11223344556677889900112233445566",
			canonicalHex,
		},
		{
			"bare-urn-btih-no-magnet-prefix",
			"urn:btih:" + canonicalHex,
			canonicalHex,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeInfohash(tc.in)
			if err != nil {
				t.Fatalf("NormalizeInfohash(%q) returned error %v, want %q", tc.in, err, tc.want)
			}
			if got != tc.want {
				t.Fatalf("NormalizeInfohash(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalizeInfohash_Skip locks the rejection branches: every in-scope skip must
// return ("", ErrSkipInfohash) — never a different error, never a partial value, never
// a panic. These are the branches the conformance suite does not assert (empty/whitespace,
// non-40-hex, undecodable/wrong-length base32, pure-v2 btmh-only).
func TestNormalizeInfohash_Skip(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace-only", "   \t\n"},
		{"too-short-hex", "4b7e3a050b355055f3cd149c5e68d9ea4576b6"},                 // 38 chars
		{"too-long-hex", canonicalHex + "ab"},                                       // 42 chars
		{"40-chars-non-hex", "zzzz3a050b355055f3cd149c5e68d9ea4576b692"},            // 40 chars, has non-hex
		{"40-chars-with-space", "4b7e3a050b355055 3cd149c5e68d9ea4576b692"},         // 40 chars, embedded space
		{"32-chars-undecodable-base32-digits", "11111111111111111111111111111111"},  // '1' is not in the RFC4648 base32 alphabet
		{"32-chars-undecodable-base32-symbols", "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@"}, // non-alphabet symbols, 32 chars
		{"pure-v2-magnet-btmh-only", "magnet:?xt=urn:btmh:1220deadbeef00112233445566778899aabbccddeeff00112233445566778899aabb"},
		{"magnet-no-btih-no-btmh", "magnet:?dn=Some.Release&tr=udp://x"},
		{"garbage", "not-an-infohash"},
		{"wrong-length-16", "0123456789abcdef"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeInfohash(tc.in)
			if got != "" {
				t.Fatalf("NormalizeInfohash(%q) = %q, want \"\" on skip", tc.in, got)
			}
			if !errors.Is(err, ErrSkipInfohash) {
				t.Fatalf("NormalizeInfohash(%q) err = %v, want ErrSkipInfohash", tc.in, err)
			}
		})
	}
}

// TestNormalizeInfohash_Idempotent guards the dedup invariant directly: normalizing an
// already-canonical value (or a re-normalization of any accepted form) is a fixed point.
func TestNormalizeInfohash_Idempotent(t *testing.T) {
	for _, in := range []string{canonicalHex, strings.ToUpper(canonicalHex), base32Btih, "magnet:?xt=urn:btih:" + base32Btih} {
		first, err := NormalizeInfohash(in)
		if err != nil {
			t.Fatalf("NormalizeInfohash(%q): %v", in, err)
		}
		second, err := NormalizeInfohash(first)
		if err != nil {
			t.Fatalf("re-NormalizeInfohash(%q): %v", first, err)
		}
		if first != second {
			t.Fatalf("not idempotent: NormalizeInfohash(%q)=%q then NormalizeInfohash(%q)=%q", in, first, first, second)
		}
		if first != canonicalHex {
			t.Fatalf("NormalizeInfohash(%q) = %q, want canonical %q", in, first, canonicalHex)
		}
	}
}
