package mcp

import (
	_ "embed"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed tool_resolve_magnets.md
var toolResolveMagnetsDoc string

func addResolveMagnetsTool(srv *mcpsdk.Server, s *Server) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "resolve_magnets",
		Description: forLLM(toolResolveMagnetsDoc),
		Annotations: readOnlyToolAnnotations(),
		InputSchema: objectSchema,
	}, toolHandler("resolve_magnets", s.metrics, s.dispatch.ResolveMagnets))
}
