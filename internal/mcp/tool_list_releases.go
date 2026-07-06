package mcp

import (
	"context"
	_ "embed"
	"log/slog"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wyvernzora/takuhai/internal/dispatch"
)

//go:embed tool_list_releases.md
var toolListReleasesDoc string

func addListReleasesTool(srv *mcpsdk.Server, s *Server) {
	addStructuredTool[dispatch.ListReleasesRequest, dispatch.ListReleasesResult](srv, &mcpsdk.Tool{
		Name:        "list_releases",
		Description: forLLM(toolListReleasesDoc),
		Annotations: readOnlyToolAnnotations(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input dispatch.ListReleasesRequest) (*mcpsdk.CallToolResult, dispatch.ListReleasesResult, error) {
		start := time.Now()
		out, err := s.dispatch.ListReleasesTyped(ctx, input)
		if err != nil {
			dur := time.Since(start)
			s.metrics.MCPTool("list_releases", "error", dur)
			s.log(ctx, slog.LevelWarn, "mcp tool failed",
				"tool", "list_releases",
				"ref_present", input.Ref != "",
				"ref_namespace", refNamespace(input.Ref),
				"limit", input.Limit,
				"has_cursor", input.Cursor != "",
				"has_since", input.Since != nil,
				"code", dispatch.WireCode(err),
				"duration_ms", dur.Milliseconds(),
				"err", err,
			)
			return errorResult(err), dispatch.ListReleasesResult{}, nil
		}
		dur := time.Since(start)
		s.metrics.MCPTool("list_releases", "ok", dur)
		s.log(ctx, slog.LevelInfo, "mcp tool completed",
			"tool", "list_releases",
			"ref_namespace", refNamespace(input.Ref),
			"limit", input.Limit,
			"has_cursor", input.Cursor != "",
			"has_since", input.Since != nil,
			"release_count", len(out.Releases),
			"has_next_cursor", out.NextCursor != nil,
			"duration_ms", dur.Milliseconds(),
		)
		return nil, out, nil
	})
}

func refNamespace(ref string) string {
	ns, _, ok := strings.Cut(ref, ":")
	if !ok {
		return ""
	}
	return ns
}
