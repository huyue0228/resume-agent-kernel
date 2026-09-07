package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/jsonschema-go/jsonschema"
	"sort"

	"resume-agent-kernel/internal/protocol"
)

// Definition is the model-facing, transport-neutral description of one tool.
type Definition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
	Provider    string `json:"provider"`
	ReadOnly    bool   `json:"read_only"`
	Visibility  string `json:"visibility,omitempty"`
	Version     string `json:"version,omitempty"`
}

type Result struct {
	Payload   json.RawMessage
	ItemCount int
}

// Provider lets built-in capabilities and external MCP servers share one
// allowlisted execution boundary.
type Provider interface {
	Name() string
	Definitions() []Definition
	Execute(context.Context, protocol.ToolCall) (Result, error)
}

type Registry struct {
	definitions map[string]Definition
	providers   map[string]Provider
	ordered     []Definition
	frozen      bool
	schemas     map[string]*jsonschema.Resolved
}

func NewProviders(providers ...Provider) (*Registry, error) {
	registry := &Registry{
		definitions: make(map[string]Definition),
		providers:   make(map[string]Provider),
		schemas:     make(map[string]*jsonschema.Resolved),
	}
	for _, provider := range providers {
		if err := registry.Register(provider); err != nil {
			return nil, err
		}
	}
	registry.Freeze()
	return registry, nil
}

func (registry *Registry) Register(provider Provider) error {
	if registry.frozen {
		return fmt.Errorf("tool registry is frozen")
	}
	if provider == nil || provider.Name() == "" {
		return fmt.Errorf("tool provider name is required")
	}
	definitions := provider.Definitions()
	compiled := map[string]*jsonschema.Resolved{}
	for _, definition := range definitions {
		if definition.Name == "" || !definition.ReadOnly {
			return fmt.Errorf("provider %q exposes an invalid or writable tool", provider.Name())
		}
		if _, exists := registry.definitions[definition.Name]; exists {
			return fmt.Errorf("duplicate tool name %q", definition.Name)
		}
		if _, exists := compiled[definition.Name]; exists {
			return fmt.Errorf("duplicate provider tool")
		}
		schema := &jsonschema.Schema{Type: "object"}
		if definition.InputSchema != nil {
			raw, err := json.Marshal(definition.InputSchema)
			if err != nil {
				return fmt.Errorf("invalid tool schema")
			}
			if json.Unmarshal(raw, schema) != nil {
				return fmt.Errorf("invalid tool schema")
			}
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			return fmt.Errorf("invalid or external tool schema reference")
		}
		compiled[definition.Name] = resolved
	}
	for _, definition := range definitions {
		definition.Provider = provider.Name()
		registry.definitions[definition.Name] = definition
		registry.providers[definition.Name] = provider
		registry.schemas[definition.Name] = compiled[definition.Name]
		if definition.Visibility != "internal" {
			registry.ordered = append(registry.ordered, definition)
		}
	}
	return nil
}

func (registry *Registry) Freeze() {
	registry.frozen = true
	sort.Slice(registry.ordered, func(i, j int) bool {
		return registry.ordered[i].Name < registry.ordered[j].Name
	})
}

func (r *Registry) Catalog() []Definition {
	return append([]Definition(nil), r.ordered...)
}

// Fingerprint 冻结所有工具（包括内部 Provider）的名称、Schema、版本和可见性。
func (r *Registry) Fingerprint() (string, error) {
	if !r.frozen {
		return "", fmt.Errorf("registry is not frozen")
	}
	raw, err := json.Marshal(r.definitions)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (r *Registry) Execute(ctx context.Context, call protocol.ToolCall) (json.RawMessage, int, error) {
	provider, ok := r.providers[call.Name]
	if !ok || r.definitions[call.Name].Visibility == "internal" || !r.frozen {
		return nil, 0, fmt.Errorf("tool %q is not allowlisted", call.Name)
	}
	if call.Arguments == nil {
		call.Arguments = map[string]any{}
	}
	if err := r.schemas[call.Name].Validate(call.Arguments); err != nil {
		return nil, 0, fmt.Errorf("tool arguments do not match schema")
	}
	result, err := provider.Execute(ctx, call)
	return result.Payload, result.ItemCount, err
}

// ExecuteInternal 只供确定性 Provider 调用，不在模型工具目录中公开。
func (r *Registry) ExecuteInternal(ctx context.Context, call protocol.ToolCall) (json.RawMessage, error) {
	provider, ok := r.providers[call.Name]
	if !ok || !r.frozen || r.definitions[call.Name].Visibility != "internal" {
		return nil, fmt.Errorf("internal tool is unavailable")
	}
	result, err := provider.Execute(ctx, call)
	return result.Payload, err
}
