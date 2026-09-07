package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"resume-agent-kernel/internal/protocol"
)

type fakeProvider struct {
	definitions []Definition
}

func (p fakeProvider) Name() string { return "fake" }

func (p fakeProvider) Definitions() []Definition { return p.definitions }

func (p fakeProvider) Execute(_ context.Context, call protocol.ToolCall) (Result, error) {
	if call.Name != "mcp.fake.lookup" {
		return Result{}, errors.New("unexpected tool")
	}
	return Result{Payload: json.RawMessage(`{"ok":true}`), ItemCount: 1}, nil
}

func TestRegistryOnlyExecutesReadOnlyAllowlist(t *testing.T) {
	registry, err := New(protocol.CaseEnvelopeV2{Resume: protocol.ResumeContent{Text: "负责 Go 服务开发与性能优化"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Execute(context.Background(), protocol.ToolCall{Name: "database.execute_sql"}); err == nil {
		t.Fatal("expected non-allowlisted tool to be rejected")
	}
}

func TestRegistryRejectsUnknownArguments(t *testing.T) {
	registry, err := New(protocol.CaseEnvelopeV2{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Execute(context.Background(), protocol.ToolCall{
		Name:      "case.read_constraints",
		Arguments: map[string]any{"candidate_id": float64(123)},
	}); err == nil {
		t.Fatal("expected unknown argument to be rejected")
	}
}

func TestVerifyQuotesUsesNormalizedExactEvidence(t *testing.T) {
	registry, err := New(protocol.CaseEnvelopeV2{Resume: protocol.ResumeContent{Text: "负责 Go 服务开发\n与性能优化"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, count, err := registry.Execute(context.Background(), protocol.ToolCall{
		Name:      "evidence.verify_quotes",
		Arguments: map[string]any{"quotes": []any{"Go 服务开发 与性能优化"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("unexpected count: %d", count)
	}
	var result struct {
		AllVerified bool `json:"all_verified"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	if !result.AllVerified {
		t.Fatal("expected normalized quote to be verified")
	}
}

func TestRegistryComposesExternalReadOnlyProvider(t *testing.T) {
	provider := fakeProvider{definitions: []Definition{{
		Name: "mcp.fake.lookup", Description: "lookup", InputSchema: map[string]any{"type": "object"}, ReadOnly: true,
	}}}
	registry, err := New(protocol.CaseEnvelopeV2{}, provider)
	if err != nil {
		t.Fatal(err)
	}
	payload, count, err := registry.Execute(context.Background(), protocol.ToolCall{Name: "mcp.fake.lookup"})
	if err != nil || count != 1 || string(payload) != `{"ok":true}` {
		t.Fatalf("unexpected external tool result: payload=%s count=%d err=%v", payload, count, err)
	}
	if len(registry.Catalog()) != 6 {
		t.Fatalf("expected five local tools and one external tool, got %d", len(registry.Catalog()))
	}
}

func TestRegistryRejectsWritableProvider(t *testing.T) {
	provider := fakeProvider{definitions: []Definition{{Name: "mcp.fake.write", ReadOnly: false}}}
	if _, err := New(protocol.CaseEnvelopeV2{}, provider); err == nil {
		t.Fatal("expected writable provider to be rejected")
	}
}
