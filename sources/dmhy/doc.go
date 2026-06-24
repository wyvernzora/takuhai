// Package dmhy is the stateless DMHY crawler module. It parses DMHY pages and
// serves POST /crawl, emitting raw posts (pkg/rawpost.RawPost) with no infohash
// normalization and no dedup — takuhai derives the dedup key on /ingest.
//
// html.go/parse.go are the dumb parsers (HTML, 大小 size); crawl.go is
// the stateless page-walk + consecutive-empty end-of-archive threshold; server.go is
// the POST /crawl HTTP handler; cmd/takuhai-dmhy is the binary.
package dmhy
