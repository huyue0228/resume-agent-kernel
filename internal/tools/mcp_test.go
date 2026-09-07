package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"resume-agent-kernel/internal/protocol"
)

func TestMCPTransportRejectsRemotePlainHTTP(t *testing.T) {
	_, err := mcpTransport(MCPServerConfig{
		Transport: "streamable_http",
		Endpoint:  "http://mcp.example.test/rpc",
	})
	if err == nil {
		t.Fatal("expected non-loopback plain HTTP endpoint to be rejected")
	}
}

func TestMCPTransportAllowsLoopbackPlainHTTP(t *testing.T) {
	_, err := mcpTransport(MCPServerConfig{
		Transport: "streamable_http",
		Endpoint:  "http://127.0.0.1:9000/rpc",
	})
	if err != nil {
		t.Fatalf("expected loopback endpoint to be accepted: %v", err)
	}
}

func TestMCPTransportRequiresMappedSecrets(t *testing.T) {
	t.Setenv("MCP_TEST_SECRET", "")
	_, err := mcpTransport(MCPServerConfig{
		Transport:   "stdio",
		Command:     "/bin/false",
		Environment: map[string]string{"TOKEN": "MCP_TEST_SECRET_MISSING"},
	})
	if err == nil {
		t.Fatal("expected missing mapped environment variable to be rejected")
	}
}

func TestMCPProviderDiscoversAndCallsOnlyAllowlistedTool(t *testing.T) {
	ctx := context.Background()
	implementation := &mcp.Implementation{Name: "test", Version: "v1"}
	server := mcp.NewServer(implementation, nil)
	server.AddTool(
		&mcp.Tool{Name: "reference.lookup", Description: "lookup a frozen reference", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{StructuredContent: map[string]any{"value": "fixed"}}, nil
		},
	)
	server.AddTool(
		&mcp.Tool{Name: "reference.write", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		},
	)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(implementation, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider := &MCPProvider{
		name: "mcp:reference", session: clientSession, remoteNames: map[string]string{}, timeout: time.Second, maxOutputBytes: 4096,
	}
	defer provider.Close()
	if err := provider.discover(ctx, []string{"reference.lookup"}); err != nil {
		t.Fatal(err)
	}
	definitions := provider.Definitions()
	if len(definitions) != 1 || definitions[0].Name != "mcp.reference.reference.lookup" {
		t.Fatalf("unexpected definitions: %#v", definitions)
	}
	result, err := provider.Execute(ctx, protocol.ToolCall{Name: definitions[0].Name, Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["structured_content"].(map[string]any)["value"] != "fixed" {
		t.Fatalf("unexpected MCP result: %#v", payload)
	}
}
