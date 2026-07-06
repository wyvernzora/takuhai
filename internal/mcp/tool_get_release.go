package mcp

import (
	"context"
	_ "embed"
	"log/slog"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

//go:embed tool_get_release.md
var toolGetReleaseDoc string

func addGetReleaseTool(srv *mcpsdk.Server, s *Server) {
	addStructuredTool[dispatch.GetReleaseRequest, dispatch.GetReleaseResult](srv, &mcpsdk.Tool{
		Name:        "get_release",
		Description: forLLM(toolGetReleaseDoc),
		Annotations: readOnlyToolAnnotations(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input dispatch.GetReleaseRequest) (*mcpsdk.CallToolResult, dispatch.GetReleaseResult, error) {
		start := time.Now()
		out, err := s.dispatch.GetReleaseTyped(ctx, input)
		if err != nil {
			dur := time.Since(start)
			s.metrics.MCPTool("get_release", "error", dur)
			s.log(ctx, slog.LevelWarn, "mcp tool failed",
				"tool", "get_release",
				"infohash", input.Infohash,
				"code", dispatch.WireCode(err),
				"duration_ms", dur.Milliseconds(),
				"err", err,
			)
			return errorResult(err), dispatch.GetReleaseResult{}, nil
		}
		dur := time.Since(start)
		s.metrics.MCPTool("get_release", "ok", dur)
		s.log(ctx, slog.LevelInfo, "mcp tool completed",
			"tool", "get_release",
			"infohash", out.Infohash,
			"duration_ms", dur.Milliseconds(),
		)
		return nil, out, nil
	})
}
