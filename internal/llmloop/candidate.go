package llmloop

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"resume-agent-kernel/internal/agent"
	"resume-agent-kernel/internal/model"
	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/tools"
)

//go:embed instructions.txt
var instructions string

var (
	ErrInvalidOutput = errors.New("invalid model tool response")
	ErrIncomplete    = errors.New("agent reported incomplete materials")
)

func InstructionVersion() string {
	digest := sha256.Sum256([]byte(instructions))
	return "sha256:" + hex.EncodeToString(digest[:])
}

type toolResponse struct {
	Kind  string       `json:"kind"`
	Calls []p.ToolCall `json:"tool_calls"`
}

func parseToolResponse(content string) (toolResponse, error) {
	// 模型外层的说明字段和调用 ID 不参与执行；只提取执行字段。
	// 工具 arguments 仍由 Registry 按每个工具的严格 Schema 校验。
	var wire struct {
		Kind  string `json:"kind"`
		Calls []struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(content), &wire); err != nil || wire.Kind != "tool_calls" || len(wire.Calls) == 0 {
		return toolResponse{}, ErrInvalidOutput
	}
	response := toolResponse{Kind: wire.Kind, Calls: make([]p.ToolCall, len(wire.Calls))}
	for i, call := range wire.Calls {
		response.Calls[i] = p.ToolCall{Name: call.Name, Arguments: call.Arguments}
	}
	return response, nil
}

func RunCandidate(ctx context.Context, client model.Client, registry *tools.Registry, c *agent.Collector, b p.TaskBudgetV1, trace *p.SafeTrace) error {
	catalog, _ := json.Marshal(map[string]any{"available_tools": registry.Catalog(), "budget": b, "existing_profile": c.Profile, "profile_already_submitted": c.Profile != nil})
	messages := []model.Message{{Role: "system", Content: instructions}, {Role: "user", Content: string(catalog)}}
	repairs := 0
	repairOutput := func(content string) bool {
		if repairs >= 2 || trace.Turns >= b.MaxTurns {
			return false
		}
		repairs++
		if content != "" {
			messages = append(messages, model.Message{Role: "assistant", Content: content})
		}
		messages = append(messages, model.Message{Role: "user", Content: `{"error":"本轮输出格式无效，未执行本轮任何工具。请仅返回 kind 为 tool_calls 的 JSON 对象，tool_calls 为非空数组，每项只包含 id、name、arguments。继续尚未完成的工具调用，不要重复已成功提交的画像或岗位。"}`})
		return true
	}
	allowedNames := map[string]bool{}
	for _, definition := range registry.Catalog() {
		allowedNames[definition.Name] = true
	}
	for trace.Turns < b.MaxTurns {
		if err := ctx.Err(); err != nil {
			return err
		}
		if trace.InputTokens+trace.OutputTokens >= b.MaxTokens {
			return errors.New("token budget exceeded")
		}
		estimatedInput := 0
		for _, m := range messages {
			estimatedInput += len(m.Content) + 16
		}
		remaining := b.MaxTokens - trace.InputTokens - trace.OutputTokens - estimatedInput
		if remaining < 128 {
			return errors.New("token budget insufficient for next request")
		}
		if httpClient, ok := client.(*model.HTTPClient); ok {
			httpClient.OutputBudget = min(remaining, 8192)
		}
		content, usage, err := client.Complete(ctx, messages)
		if usage.InputTokens == 0 {
			usage.InputTokens = estimatedInput
		}
		if usage.OutputTokens == 0 {
			usage.OutputTokens = len(content)
		}
		trace.Turns++
		trace.InputTokens += usage.InputTokens
		trace.OutputTokens += usage.OutputTokens
		if err != nil {
			if errors.Is(err, model.ErrInvalidResponse) && repairOutput(content) {
				continue
			}
			return err
		}
		if trace.InputTokens+trace.OutputTokens > b.MaxTokens {
			return errors.New("token budget exceeded")
		}
		response, parseErr := parseToolResponse(content)
		if parseErr != nil {
			if repairOutput(content) {
				continue
			}
			return ErrInvalidOutput
		}
		// ID 仅关联对话中的调用与结果，由运行时编号，避免模型跨轮重用或漏填。
		// 同步改写 assistant 历史，保证模型看到的调用和返回仍一一对应。
		for i := range response.Calls {
			response.Calls[i].ID = fmt.Sprintf("call_%d_%d", trace.Turns, i+1)
		}
		normalized, _ := json.Marshal(response)
		messages = append(messages, model.Message{Role: "assistant", Content: string(normalized)})
		for _, call := range response.Calls {
			if c.Done {
				return ErrInvalidOutput
			}
			if trace.ToolCallCount >= b.MaxToolCalls {
				return errors.New("tool budget exceeded")
			}
			trace.ToolCallCount++
			start := time.Now()
			raw, count, err := registry.Execute(ctx, call)
			status := "ok"
			if err != nil {
				status = "error"
				raw, _ = json.Marshal(map[string]string{"error": "tool arguments, scope or evidence invalid; inspect schema and correct submission"})
			}
			traceName := call.Name
			if !allowedNames[traceName] {
				traceName = "unregistered_tool"
			}
			trace.ToolCalls = append(trace.ToolCalls, p.ToolTrace{Name: traceName, Status: status, DurationMS: time.Since(start).Milliseconds(), ItemCount: count})
			msg, _ := json.Marshal(map[string]any{"tool_call_id": call.ID, "result": json.RawMessage(raw)})
			messages = append(messages, model.Message{Role: "user", Content: string(msg)})
		}
		if c.Done {
			if c.Failed {
				return ErrIncomplete
			}
			return nil
		}
	}
	return errors.New("turn budget exhausted without task_done")
}
