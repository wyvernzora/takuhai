package mcp

import (
	"context"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wyvernzora/takuhai/internal/store"
)

func TestNewServerAdvertisesInstructions(t *testing.T) {
	session := newTestMCPSession(t)
	got := session.InitializeResult().Instructions

	for _, want := range []string{
		"Takuhai is a passive anime release index",
		"read-only and consumer-facing",
		"list_releases",
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
		"list_releases":   "Return matched releases for one canonical ref",
		"resolve_magnets": "Resolve infohashes into stored magnet URIs",
	} {
		got := descriptions[name]
		if !strings.Contains(got, want) {
			t.Fatalf("%s description missing %q:\n%s", name, want, got)
		}
	}
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
func (f *fakeStore) ListReleases(context.Context, store.ReleaseQuery) (store.ReleasePage, error) {
	return store.ReleasePage{}, nil
}
func (f *fakeStore) ResolveMagnets(context.Context, []string) (map[string]string, error) {
	return nil, nil
}
func (f *fakeStore) Close() error { return nil }
