package mcp

import (
	"regexp"
	"strings"
)

var (
	reSchema     = regexp.MustCompile(`(?s)<!--\s*schema\s*-->.*?<!--\s*/schema\s*-->`)
	reSchemaNote = regexp.MustCompile(`(?s)<!--\s*schema-note\b.*?-->`)
)

// forLLM strips human-doc-only schema blocks before embedding markdown into MCP
// descriptions. Dispatch owns wire validation, so duplicated schema notes in prose are
// treated as authoring scaffolding.
func forLLM(raw string) string {
	out := reSchema.ReplaceAllString(raw, "")
	out = reSchemaNote.ReplaceAllString(out, "")
	return strings.TrimSpace(out)
}
