// Package nyaa is the stateless Nyaa crawler module. It parses Nyaa listing pages
// and serves POST /crawl, emitting raw posts (pkg/rawpost.RawPost) with no
// infohash normalization or matching policy.
package nyaa
