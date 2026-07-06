package mcp

import (
	"context"
	_ "embed"
	"log/slog"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

//go:embed tool_resolve_magnets.md
var toolResolveMagnetsDoc string

func addResolveMagnetsTool(srv *mcpsdk.Server, s *Server) {
	addStructuredTool[dispatch.ResolveMagnetsRequest, dispatch.ResolveMagnetsResult](srv, &mcpsdk.Tool{
		Name:        "resolve_magnets",
		Description: forLLM(toolResolveMagnetsDoc),
		Annotations: readOnlyToolAnnotations(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input dispatch.ResolveMagnetsRequest) (*mcpsdk.CallToolResult, dispatch.ResolveMagnetsResult, error) {
		start := time.Now()
		out, err := s.dispatch.ResolveMagnetsTyped(ctx, input)
		if err != nil {
			dur := time.Since(start)
			s.metrics.MCPTool("resolve_magnets", "error", dur)
			s.log(ctx, slog.LevelWarn, "mcp tool failed",
				"tool", "resolve_magnets",
				"input_count", len(input.Infohashes),
				"code", dispatch.WireCode(err),
				"duration_ms", dur.Milliseconds(),
				"err", err,
			)
			return errorResult(err), dispatch.ResolveMagnetsResult{}, nil
		}
		dur := time.Since(start)
		s.metrics.MCPTool("resolve_magnets", "ok", dur)
		misses := len(input.Infohashes) - len(out.Magnets)
		if misses < 0 {
			misses = 0
		}
		s.metrics.MCPResolveMagnets(len(out.Magnets), misses)
		s.log(ctx, slog.LevelInfo, "mcp tool completed",
			"tool", "resolve_magnets",
			"input_count", len(input.Infohashes),
			"hit_count", len(out.Magnets),
			"miss_count", misses,
			"duration_ms", dur.Milliseconds(),
		)
		return nil, out, nil
	})
}
