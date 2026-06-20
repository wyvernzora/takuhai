// Package mcp wires the single MCP server that exposes two tool groups: the
// consumer tools (get_releases by canonical ref) and the worker tools
// (claim_unmatched, submit_match, escalate, defer, reopen, get_queue_stats)
// the external matching agent drives, with lease/visibility semantics.
// Transport: streamable HTTP at /mcp plus stdio for local use.
//
// Implementation lands in Phase 2 (worker) and Phase 3 (consumer); see
// docs/indexer-handover.md §9 and §10.
package mcp
