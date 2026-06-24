// Package mcp wires the CONSUMER-ONLY MCP server: the agent-facing read surface
// over the catalog — list_releases (matched releases for a canonical ref,
// cursor-paginated) and resolve_magnets (resolve infohash values into stored magnet
// URIs). Queue mutation stays behind the REST API (internal/rest). Transport is
// streamable HTTP at /mcp.
package mcp
