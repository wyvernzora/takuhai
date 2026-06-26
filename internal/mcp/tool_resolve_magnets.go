package mcp

import (
	"context"
	_ "embed"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

//go:embed tool_resolve_magnets.md
var toolResolveMagnetsDoc string

func addResolveMagnetsTool(srv *mcpsdk.Server, s *Server) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "resolve_magnets",
		Description: forLLM(toolResolveMagnetsDoc),
		Annotations: readOnlyToolAnnotations(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input dispatch.ResolveMagnetsRequest) (*mcpsdk.CallToolResult, dispatch.ResolveMagnetsResult, error) {
		start := time.Now()
		out, err := s.dispatch.ResolveMagnetsTyped(ctx, input)
		if err != nil {
			s.metrics.MCPTool("resolve_magnets", "error", time.Since(start))
			return errorResult(err), dispatch.ResolveMagnetsResult{}, nil
		}
		s.metrics.MCPTool("resolve_magnets", "ok", time.Since(start))
		misses := len(input.Infohashes) - len(out.Magnets)
		if misses < 0 {
			misses = 0
		}
		s.metrics.MCPResolveMagnets(len(out.Magnets), misses)
		return nil, out, nil
	})
}
