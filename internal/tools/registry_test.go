package tools

import (
	"context"
	"encoding/json"
	"resume-agent-kernel/internal/protocol"
	"testing"
)

type fakeProvider struct{ definitions []Definition }

func (p fakeProvider) Name() string              { return "fake" }
func (p fakeProvider) Definitions() []Definition { return p.definitions }
func (p fakeProvider) Execute(_ context.Context, _ protocol.ToolCall) (Result, error) {
	return Result{Payload: json.RawMessage(`{"ok":true}`), ItemCount: 1}, nil
}
func provider() fakeProvider {
	return fakeProvider{[]Definition{{
		Name: "mcp.fake.lookup", Version: "v1", ReadOnly: true,
		InputSchema: map[string]any{"type": "object", "additionalProperties": false},
	}}}
}
func TestRegistryEnforcesScopeSchemaAndFreeze(t *testing.T) {
	r, err := NewProviders(provider())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Execute(context.Background(), protocol.ToolCall{Name: "database.write"}); err == nil {
		t.Fatal("out of scope accepted")
	}
	if _, _, err := r.Execute(context.Background(), protocol.ToolCall{Name: "mcp.fake.lookup", Arguments: map[string]any{"candidate_id": 1}}); err == nil {
		t.Fatal("unknown argument accepted")
	}
	if err := r.Register(provider()); err == nil {
		t.Fatal("frozen registry changed")
	}
	raw, count, err := r.Execute(context.Background(), protocol.ToolCall{Name: "mcp.fake.lookup"})
	if err != nil || count != 1 || string(raw) != `{"ok":true}` {
		t.Fatalf("bad result %s %v", raw, err)
	}
}
func TestRegistryRejectsWritableAndConflictingProviders(t *testing.T) {
	p := provider()
	p.definitions[0].ReadOnly = false
	if _, err := NewProviders(p); err == nil {
		t.Fatal("writable provider accepted")
	}
	if _, err := NewProviders(provider(), provider()); err == nil {
		t.Fatal("conflict accepted")
	}
}
func TestInternalToolsAreHiddenAndIncludedInVersion(t *testing.T) {
	p := provider()
	p.definitions[0].Visibility = "internal"
	r, _ := NewProviders(p)
	if len(r.Catalog()) != 0 {
		t.Fatal("internal tool exposed")
	}
	if _, _, err := r.Execute(context.Background(), protocol.ToolCall{Name: "mcp.fake.lookup"}); err == nil {
		t.Fatal("agent called internal tool")
	}
	if _, err := r.ExecuteInternal(context.Background(), protocol.ToolCall{Name: "mcp.fake.lookup"}); err != nil {
		t.Fatal(err)
	}
	first, _ := r.Fingerprint()
	p.definitions[0].Version = "v2"
	updated, _ := NewProviders(p)
	second, _ := updated.Fingerprint()
	if first == second {
		t.Fatal("internal provider version not pinned")
	}
}
