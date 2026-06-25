package mcp

import (
	_ "embed"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed tool_list_releases.md
var toolListReleasesDoc string

func addListReleasesTool(srv *mcpsdk.Server, s *Server) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "list_releases",
		Description: forLLM(toolListReleasesDoc),
		Annotations: readOnlyToolAnnotations(),
		InputSchema: objectSchema,
	}, toolHandler("list_releases", s.metrics, s.dispatch.ListReleases))
}
