package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wyvernzora/takuhai/internal/cursor"
	"github.com/wyvernzora/takuhai/internal/store"
)

func TestNewServerAdvertisesInstructions(t *testing.T) {
	session := newTestMCPSession(t)
	got := session.InitializeResult().Instructions

	for _, want := range []string{
		"Takuhai is a passive anime release index",
		"read-only and consumer-facing",
		"list_releases",
		"get_release",
		"resolve_magnets",
		"Infohashes are release identity",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("instructions missing %q:\n%s", want, got)
		}
	}
}

func TestToolsAdvertiseEmbeddedDocs(t *testing.T) {
	session := newTestMCPSession(t)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	descriptions := map[string]string{}
	for _, tool := range res.Tools {
		descriptions[tool.Name] = tool.Description
	}
	for name, want := range map[string]string{
		"get_release":     "Fetch one release by infohash",
		"list_releases":   "Return matched releases, newest first",
		"resolve_magnets": "Resolve infohashes into stored magnet URIs",
	} {
		got := descriptions[name]
		if !strings.Contains(got, want) {
			t.Fatalf("%s description missing %q:\n%s", name, want, got)
		}
	}
}

func TestToolsAdvertiseTypedInputSchemas(t *testing.T) {
	session := newTestMCPSession(t)
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	schemas := map[string]map[string]any{}
	for _, tool := range res.Tools {
		b, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal %s schema: %v", tool.Name, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatalf("decode %s schema: %v", tool.Name, err)
		}
		schemas[tool.Name] = schema
	}

	assertOptionalProperty(t, schemas["list_releases"], "ref")
	assertOptionalProperty(t, schemas["list_releases"], "limit")
	assertOptionalProperty(t, schemas["list_releases"], "cursor")
	assertRequiredProperty(t, schemas["get_release"], "infohash")
	assertRequiredProperty(t, schemas["resolve_magnets"], "infohashes")
}

func TestListReleasesPreservesTaxonomyErrors(t *testing.T) {
	session := newTestMCPSession(t)
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "list_releases",
		Arguments: map[string]any{"ref": "not-a-ref"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError = false, content = %v", res.Content)
	}
	if got := firstText(res); !strings.Contains(got, `"code":"invalid_ref"`) {
		t.Fatalf("error content = %s, want invalid_ref code", got)
	}
}

func TestGetReleaseErrorOmitsStructuredContentAndSuccessKeepsIt(t *testing.T) {
	session := newTestMCPSession(t)
	unknown := strings.Repeat("0", 40)
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "get_release",
		Arguments: map[string]any{"infohash": unknown},
	})
	if err != nil {
		t.Fatalf("CallTool unknown: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError = false, content = %v", res.Content)
	}
	if res.StructuredContent != nil {
		t.Fatalf("StructuredContent = %#v, want nil on tool error", res.StructuredContent)
	}
	if got := firstText(res); !strings.Contains(got, `"code":"no_such_release"`) {
		t.Fatalf("error content = %s, want no_such_release code", got)
	}

	known := strings.Repeat("1", 40)
	res, err = session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "get_release",
		Arguments: map[string]any{"infohash": known},
	})
	if err != nil {
		t.Fatalf("CallTool known: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, content = %v", res.Content)
	}
	content, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent = %T, want map[string]any", res.StructuredContent)
	}
	if got := content["infohash"]; got != known {
		t.Fatalf("infohash = %v, want %s", got, known)
	}
	if _, ok := content["raw_items"].([]any); !ok {
		t.Fatalf("raw_items = %#v, want array", content["raw_items"])
	}
	if _, ok := content["match_events"].([]any); !ok {
		t.Fatalf("match_events = %#v, want array", content["match_events"])
	}
}

func firstText(res *mcpsdk.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func TestForLLMStripsHumanOnlySchemaBlocks(t *testing.T) {
	raw := `Intro.

<!-- schema-note
Human-only implementation note.
-->

Middle.

<!-- schema -->
## Parameters

- field docs
<!-- /schema -->

## Response

Outro.`

	got := forLLM(raw)
	for _, unwanted := range []string{"schema-note", "Human-only", "field docs", "<!--", "## Parameters"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("forLLM output contains %q:\n%s", unwanted, got)
		}
	}
	for _, wanted := range []string{"Intro.", "Middle.", "## Response", "Outro."} {
		if !strings.Contains(got, wanted) {
			t.Fatalf("forLLM output missing %q:\n%s", wanted, got)
		}
	}
}

func newTestMCPSession(t *testing.T) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := NewServer(&fakeStore{}, nil)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	st, ct := mcpsdk.NewInMemoryTransports()

	srvSession, err := server.sdk.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { srvSession.Close() })

	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { clientSession.Close() })
	if clientSession.InitializeResult() == nil {
		t.Fatal("InitializeResult is nil")
	}
	return clientSession
}

func assertRequiredProperty(t *testing.T, schema map[string]any, name string) {
	t.Helper()
	assertProperty(t, schema, name)
	for _, v := range schema["required"].([]any) {
		if v == name {
			return
		}
	}
	t.Fatalf("schema required = %v, missing %q", schema["required"], name)
}

func assertOptionalProperty(t *testing.T, schema map[string]any, name string) {
	t.Helper()
	assertProperty(t, schema, name)
	required, _ := schema["required"].([]any)
	for _, v := range required {
		if v == name {
			t.Fatalf("schema required = %v, want %q optional", required, name)
		}
	}
}

func assertProperty(t *testing.T, schema map[string]any, name string) {
	t.Helper()
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props[name]; !ok {
		t.Fatalf("schema properties = %v, missing %q", props, name)
	}
}

type fakeStore struct{}

func (f *fakeStore) Ping(context.Context) error { return nil }
func (f *fakeStore) IngestN(context.Context, store.IngestParams) (store.IngestOutcome, error) {
	return store.IngestOutcome{}, nil
}
func (f *fakeStore) Claim(context.Context, store.ClaimParams) (store.ClaimResult, error) {
	return store.ClaimResult{}, nil
}
func (f *fakeStore) Submit(context.Context, store.SubmitParams) error { return nil }
func (f *fakeStore) QueueStats(context.Context) (store.QueueStats, error) {
	return store.QueueStats{}, nil
}
func (f *fakeStore) CatalogStats(context.Context) (store.CatalogStats, error) {
	return store.CatalogStats{}, nil
}
func (f *fakeStore) ListReleases(_ context.Context, q store.ReleaseQuery) (store.ReleasePage, error) {
	if q.Ref != "" {
		if err := cursor.ValidateRef(q.Ref); err != nil {
			return store.ReleasePage{}, err
		}
	}
	return store.ReleasePage{}, nil
}
func (f *fakeStore) GetRelease(_ context.Context, infohash string) (store.ReleaseDetail, error) {
	if infohash == strings.Repeat("0", 40) {
		return store.ReleaseDetail{}, store.ErrNoSuchRelease
	}
	return store.ReleaseDetail{
		Infohash:    infohash,
		Title:       "release",
		PublishedAt: time.Unix(1, 0).UTC(),
		MatchStatus: "unmatched",
		CreatedAt:   time.Unix(1, 0).UTC(),
		UpdatedAt:   time.Unix(1, 0).UTC(),
	}, nil
}
func (f *fakeStore) ResolveMagnets(context.Context, []string) (map[string]string, error) {
	return nil, nil
}
func (f *fakeStore) Close() error { return nil }
