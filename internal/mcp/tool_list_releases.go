package mcp

import (
	"context"
	_ "embed"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

//go:embed tool_list_releases.md
var toolListReleasesDoc string

func addListReleasesTool(srv *mcpsdk.Server, s *Server) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_releases",
		Description: forLLM(toolListReleasesDoc),
		Annotations: readOnlyToolAnnotations(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input dispatch.ListReleasesRequest) (*mcpsdk.CallToolResult, dispatch.ListReleasesResult, error) {
		start := time.Now()
		out, err := s.dispatch.ListReleasesTyped(ctx, input)
		if err != nil {
			s.metrics.MCPTool("list_releases", "error", time.Since(start))
			return errorResult(err), dispatch.ListReleasesResult{}, nil
		}
		s.metrics.MCPTool("list_releases", "ok", time.Since(start))
		return nil, out, nil
	})
}
