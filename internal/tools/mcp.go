package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"resume-agent-kernel/internal/protocol"
)

const (
	defaultMCPTimeout        = 10 * time.Second
	defaultMCPMaxOutputBytes = 64 << 10
	maxMCPConfigBytes        = 1 << 20
)

var mcpNamespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

type MCPConfig struct {
	Servers []MCPServerConfig `json:"servers"`
}

type MCPServerConfig struct {
	Purpose        string            `json:"purpose,omitempty"`
	Visibility     string            `json:"visibility,omitempty"`
	Name           string            `json:"name"`
	Transport      string            `json:"transport"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Endpoint       string            `json:"endpoint,omitempty"`
	Environment    map[string]string `json:"environment_from_env,omitempty"`
	Headers        map[string]string `json:"headers_from_env,omitempty"`
	AllowTools     []string          `json:"allow_tools"`
	ReadOnly       bool              `json:"read_only"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int               `json:"max_output_bytes,omitempty"`
}

type MCPProvider struct {
	secrets        []string
	name           string
	session        *mcp.ClientSession
	definitions    []Definition
	remoteNames    map[string]string
	timeout        time.Duration
	maxOutputBytes int
}

func LoadMCPProviders(ctx context.Context, path string, logger *slog.Logger) ([]Provider, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open MCP config: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxMCPConfigBytes+1))
	decoder.DisallowUnknownFields()
	var config MCPConfig
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode MCP config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("MCP config contains trailing data")
	}
	if len(config.Servers) > 16 {
		return nil, errors.New("MCP config supports at most 16 servers")
	}
	providers := make([]Provider, 0, len(config.Servers))
	seen := make(map[string]struct{}, len(config.Servers))
	for _, server := range config.Servers {
		if _, ok := seen[server.Name]; ok {
			closeProviders(providers)
			return nil, fmt.Errorf("duplicate MCP server name %q", server.Name)
		}
		seen[server.Name] = struct{}{}
		provider, err := connectMCPProvider(ctx, server, logger)
		if err != nil {
			closeProviders(providers)
			return nil, fmt.Errorf("initialize MCP server %q: %w", server.Name, err)
		}
		providers = append(providers, provider)
	}
	return providers, nil
}

func connectMCPProvider(ctx context.Context, config MCPServerConfig, logger *slog.Logger) (*MCPProvider, error) {
	if config.Purpose != "taxonomy" && config.Purpose != "job_knowledge" && config.Purpose != "ocr" {
		return nil, errors.New("MCP purpose must be taxonomy, job_knowledge or ocr")
	}
	if config.Purpose == "ocr" && config.Visibility != "internal" {
		return nil, errors.New("OCR MCP must be internal")
	}
	if config.Visibility != "" && config.Visibility != "internal" && config.Visibility != "agent" {
		return nil, errors.New("invalid MCP visibility")
	}
	if !mcpNamespacePattern.MatchString(config.Name) {
		return nil, errors.New("name must match ^[a-z][a-z0-9_-]{0,31}$")
	}
	if !config.ReadOnly {
		return nil, errors.New("read_only must be true; business writes are not allowed")
	}
	if len(config.AllowTools) == 0 || len(config.AllowTools) > 64 {
		return nil, errors.New("allow_tools must contain 1 to 64 explicit tool names")
	}
	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = defaultMCPTimeout
	}
	if timeout < time.Second || timeout > 120*time.Second {
		return nil, errors.New("timeout_seconds must be between 1 and 120")
	}
	maxOutput := config.MaxOutputBytes
	if maxOutput == 0 {
		maxOutput = defaultMCPMaxOutputBytes
	}
	if maxOutput < 1024 || maxOutput > 1<<20 {
		return nil, errors.New("max_output_bytes must be between 1024 and 1048576")
	}
	transport, err := mcpTransport(config)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "smart-resume-agent-kernel", Version: protocol.ProtocolVersion},
		&mcp.ClientOptions{Logger: logger, Capabilities: &mcp.ClientCapabilities{}},
	)
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, err
	}
	provider := &MCPProvider{
		name:           "mcp:" + config.Name,
		session:        session,
		remoteNames:    make(map[string]string),
		timeout:        timeout,
		maxOutputBytes: maxOutput,
	}
	for _, mapping := range []map[string]string{config.Environment, config.Headers} {
		for _, envName := range mapping {
			if secret := os.Getenv(envName); secret != "" {
				provider.secrets = append(provider.secrets, secret)
			}
		}
	}
	if err := provider.discover(connectCtx, config.AllowTools); err != nil {
		_ = session.Close()
		return nil, err
	}
	for i := range provider.definitions {
		provider.definitions[i].Visibility = config.Visibility
		if info := session.InitializeResult().ServerInfo; info != nil {
			provider.definitions[i].Version = info.Version
		}
	}
	return provider, nil
}

func mcpTransport(config MCPServerConfig) (mcp.Transport, error) {
	switch config.Transport {
	case "stdio":
		if strings.TrimSpace(config.Command) == "" || config.Endpoint != "" || len(config.Headers) != 0 {
			return nil, errors.New("stdio transport requires command and forbids endpoint/headers")
		}
		command := exec.Command(config.Command, config.Args...)
		command.Env = os.Environ()
		for childName, hostName := range config.Environment {
			if childName == "" || hostName == "" {
				return nil, errors.New("environment_from_env entries require non-empty names")
			}
			value, ok := os.LookupEnv(hostName)
			if !ok {
				return nil, fmt.Errorf("required environment variable %q is missing", hostName)
			}
			command.Env = append(command.Env, childName+"="+value)
		}
		return &mcp.CommandTransport{Command: command}, nil
	case "streamable_http":
		if config.Command != "" || len(config.Args) != 0 || len(config.Environment) != 0 {
			return nil, errors.New("streamable_http transport forbids command/args/environment")
		}
		parsed, err := url.Parse(config.Endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname()))) {
			return nil, errors.New("endpoint must use HTTPS, except for loopback HTTP")
		}
		headers := make(http.Header)
		for name, hostName := range config.Headers {
			value, ok := os.LookupEnv(hostName)
			if !ok {
				return nil, fmt.Errorf("required header environment variable %q is missing", hostName)
			}
			headers.Set(name, value)
		}
		return &mcp.StreamableClientTransport{
			Endpoint: config.Endpoint,
			HTTPClient: &http.Client{
				Timeout:   2 * time.Minute,
				Transport: headerTransport{base: http.DefaultTransport, headers: headers},
			},
			DisableStandaloneSSE: true,
		}, nil
	default:
		return nil, errors.New("transport must be stdio or streamable_http")
	}
}

func (p *MCPProvider) discover(ctx context.Context, allowTools []string) error {
	allowed := make(map[string]struct{}, len(allowTools))
	for _, name := range allowTools {
		name = strings.TrimSpace(name)
		if name == "" || strings.Contains(name, "*") {
			return errors.New("allow_tools must use explicit non-wildcard names")
		}
		if _, exists := allowed[name]; exists {
			return fmt.Errorf("duplicate allowlisted tool %q", name)
		}
		allowed[name] = struct{}{}
	}
	discovered := make(map[string]*mcp.Tool)
	cursor := ""
	for {
		result, err := p.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return err
		}
		for _, tool := range result.Tools {
			if tool != nil {
				discovered[tool.Name] = tool
			}
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	for remoteName := range allowed {
		tool, ok := discovered[remoteName]
		if !ok {
			return fmt.Errorf("allowlisted tool %q was not advertised", remoteName)
		}
		if tool.Annotations != nil && !tool.Annotations.ReadOnlyHint {
			return errors.New("MCP tool advertises side effects")
		}
		exposed := "mcp." + strings.TrimPrefix(p.name, "mcp:") + "." + remoteName
		p.remoteNames[exposed] = remoteName
		p.definitions = append(p.definitions, Definition{
			Name: exposed, Description: tool.Description, InputSchema: tool.InputSchema, ReadOnly: true,
		})
	}
	sort.Slice(p.definitions, func(i, j int) bool { return p.definitions[i].Name < p.definitions[j].Name })
	return nil
}

func (p *MCPProvider) Name() string { return p.name }

func (p *MCPProvider) Definitions() []Definition {
	return append([]Definition(nil), p.definitions...)
}

func (p *MCPProvider) Execute(ctx context.Context, call protocol.ToolCall) (Result, error) {
	remoteName, ok := p.remoteNames[call.Name]
	if !ok {
		return Result{}, fmt.Errorf("MCP tool %q is not allowlisted", call.Name)
	}
	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	result, err := p.session.CallTool(callCtx, &mcp.CallToolParams{Name: remoteName, Arguments: call.Arguments})
	if err != nil {
		return Result{}, err
	}
	if result.NeedsInput() {
		return Result{}, errors.New("MCP elicitation is not supported")
	}
	payload, err := json.Marshal(map[string]any{
		"structured_content": result.StructuredContent,
		"content":            result.Content,
	})
	if err != nil {
		return Result{}, err
	}
	if len(payload) > p.maxOutputBytes {
		return Result{}, errors.New("MCP tool output exceeded configured limit")
	}
	if result.IsError {
		return Result{}, errors.New("MCP tool returned an error")
	}
	for _, secret := range p.secrets {
		encoded, _ := json.Marshal(secret)
		payload = bytes.ReplaceAll(payload, encoded[1:len(encoded)-1], []byte("[REDACTED]"))
	}
	return Result{Payload: payload, ItemCount: len(result.Content)}, nil
}

func (p *MCPProvider) Close() error { return p.session.Close() }

func CloseProviders(providers []Provider) error { return closeProviders(providers) }

func closeProviders(providers []Provider) error {
	var joined error
	for _, provider := range providers {
		if closer, ok := provider.(interface{ Close() error }); ok {
			joined = errors.Join(joined, closer.Close())
		}
	}
	return joined
}

type headerTransport struct {
	base    http.RoundTripper
	headers http.Header
}

func (t headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	for name, values := range t.headers {
		for _, value := range values {
			clone.Header.Add(name, value)
		}
	}
	return t.base.RoundTrip(clone)
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
