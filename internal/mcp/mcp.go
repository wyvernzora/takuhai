package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wyvernzora/takuhai/internal/dispatch"
	"github.com/wyvernzora/takuhai/internal/metrics"
	"github.com/wyvernzora/takuhai/internal/store"
)

const (
	// serverName / serverVersion identify takuhai's consumer MCP surface in the
	// SDK initialize handshake (design §6).
	serverName    = "takuhai"
	serverVersion = "0.1.0"

	// mcpEndpoint is the single streamable-HTTP path the SDK handler serves (POST
	// for client→server, GET for the server→client SSE stream — design §6).
	mcpEndpoint = "/mcp"
	// healthPath is where the standalone /healthz handler mounts alongside /mcp.
	healthPath = "/healthz"
)

// Server is the MCP server exposing the CONSUMER tool group ONLY (design
// workspace-migration §2/§4): list_releases and resolve_magnets, the agent-facing read
// surface. The worker tools moved to the REST queue/submit API (internal/rest), so the
// MCP server no longer routes them. Transport is streamable HTTP at /mcp (+ the
// standalone /healthz handler it mounts but does not own — design §6/§10).
//
// The dispatch logic lives in the transport-neutral internal/dispatch package; the
// Server holds a *dispatch.Dispatcher and routes only the consumer tools to it. The
// concrete SDK server (sdk) is built once in NewServer with the two consumer tools
// registered against that same dispatcher, so the wire surface has one source of truth
// for behavior.
type Server struct {
	dispatch *dispatch.Dispatcher
	healthz  http.Handler   // the standalone /healthz handler (design §10/§11) — mounted, not owned
	sdk      *mcpsdk.Server // the SDK server with the consumer tools registered
	metrics  *metrics.Takuhai
}

// NewServer constructs the consumer-only MCP server over the Store seam. The healthz
// argument is the standalone /healthz handler (built by internal/health from
// Store+clock): the SDK handler mounts it at /healthz alongside /mcp so there is a
// single owner of the health contract (design §6/§10). May be nil for wire tests that
// drive only /mcp tool dispatch.
func NewServer(s store.Store, healthz http.Handler) *Server {
	return NewServerWithMetrics(s, healthz, nil)
}

func NewServerWithMetrics(s store.Store, healthz http.Handler, m *metrics.Takuhai) *Server {
	d := dispatch.New(s)
	srv := &Server{dispatch: d, healthz: healthz, metrics: m}
	srv.sdk = newSDKServer(srv)
	return srv
}

// newSDKServer builds the SDK server and registers the two consumer tools. Each tool
// is wired to the matching dispatch entrypoint via the low-level Server.AddTool seam:
// the raw JSON arguments cross to the dispatch func untouched and its raw JSON result
// crosses back. The worker tools are intentionally ABSENT — they live behind
// internal/rest.
func newSDKServer(s *Server) *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, &mcpsdk.ServerOptions{
		Instructions: forLLM(serverInstructions),
	})
	addListReleasesTool(srv, s)
	addResolveMagnetsTool(srv, s)
	return srv
}

func readOnlyToolAnnotations() *mcpsdk.ToolAnnotations {
	falseVal := false
	trueVal := true
	return &mcpsdk.ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: &falseVal,
		IdempotentHint:  true,
		OpenWorldHint:   &trueVal,
	}
}

// objectSchema is the permissive {"type":"object"} input schema the low-level
// Server.AddTool requires. The dispatch funcs own input validation (they unmarshal and
// shape-check the raw JSON themselves), so the wire schema is intentionally open — the
// closed taxonomy (invalid_ref / invalid_cursor) is raised by dispatch, not by schema
// validation.
var objectSchema = json.RawMessage(`{"type":"object"}`)

// toolHandler adapts a dispatch entrypoint (raw JSON in → raw JSON out, a returned
// error carrying a closed-taxonomy wire code via dispatch.WireCode) into the SDK's
// low-level ToolHandler. On success the JSON result is returned as the tool's text
// content; on a dispatch error the result is marked IsError with the wire code as the
// payload, preserving the closed taxonomy over the wire (mirrors the in-process
// CallTool — design §6).
func toolHandler(name string, m *metrics.Takuhai, d func(context.Context, []byte) ([]byte, error)) mcpsdk.ToolHandler {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		start := time.Now()
		args := []byte(req.Params.Arguments)
		if args == nil {
			args = []byte("{}")
		}
		result, err := d(ctx, args)
		if err != nil {
			m.MCPTool(name, "error", time.Since(start))
			return errorResult(err), nil
		}
		m.MCPTool(name, "ok", time.Since(start))
		if name == "resolve_magnets" {
			m.MCPResolveMagnets(resolveMagnetCounts(args, result))
		}
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(result)}},
		}, nil
	}
}

func resolveMagnetCounts(args, result []byte) (hits, misses int) {
	var in dispatch.ResolveMagnetsRequest
	var out dispatch.ResolveMagnetsResult
	if json.Unmarshal(args, &in) != nil || json.Unmarshal(result, &out) != nil {
		return 0, 0
	}
	hits = len(out.Magnets)
	if len(in.Infohashes) > hits {
		misses = len(in.Infohashes) - hits
	}
	return hits, misses
}

// errorResult shapes a dispatch error into an MCP tool error (IsError true) carrying
// the closed-taxonomy code (design §6). The body is a small JSON object {"code","error"}
// so an agent can branch on the machine-readable code; a non-taxonomy error has an empty
// code (WireCode returns "") and surfaces only the human message.
func errorResult(err error) *mcpsdk.CallToolResult {
	body, _ := json.Marshal(struct {
		Code  string `json:"code,omitempty"`
		Error string `json:"error"`
	}{
		Code:  dispatch.WireCode(err),
		Error: err.Error(),
	})
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
	}
}

// mcpSessionTimeout reclaims idle MCP sessions: a streamable-HTTP session whose
// standalone SSE GET stream is held open but sees no traffic is auto-closed after
// this interval, so an abandoned/leaked consumer cannot pin a session (and its
// server-side request) forever. Without it (the SDK default of 0) idle sessions are
// never closed and accumulate unbounded.
const mcpSessionTimeout = 5 * time.Minute

// Handler returns the http.Handler that mounts the MCP endpoint at /mcp plus the
// standalone /healthz handler (design §6/§10). The HTTP listener sets this into its
// httpHandler hook field. /mcp is served by the SDK's streamable-HTTP transport.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mcpHandler := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return s.sdk },
		&mcpsdk.StreamableHTTPOptions{SessionTimeout: mcpSessionTimeout},
	)
	mux.Handle(mcpEndpoint, mcpHandler)
	if s.healthz != nil {
		mux.Handle(healthPath, s.healthz)
	}
	return mux
}
