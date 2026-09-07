package llmloop

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"resume-agent-kernel/internal/agent"
	"resume-agent-kernel/internal/model"
	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/tools"
)

//go:embed instructions.txt
var instructions string

func RunCandidate(ctx context.Context, client model.Client, registry *tools.Registry, c *agent.Collector, b p.TaskBudgetV1, trace *p.SafeTrace) error {
	catalog, _ := json.Marshal(map[string]any{"available_tools": registry.Catalog(), "budget": b, "existing_profile": c.Profile, "profile_already_submitted": c.Profile != nil})
	messages := []model.Message{{Role: "system", Content: instructions}, {Role: "user", Content: string(catalog)}}
	seen := map[string]bool{}
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
			return err
		}
		if trace.InputTokens+trace.OutputTokens > b.MaxTokens {
			return errors.New("token budget exceeded")
		}
		var response struct {
			Kind  string       `json:"kind"`
			Calls []p.ToolCall `json:"tool_calls"`
		}
		if err := agent.Decode([]byte(content), &response); err != nil {
			return err
		}
		if response.Kind != "tool_calls" || len(response.Calls) == 0 {
			return errors.New("result tools and task_done required")
		}
		messages = append(messages, model.Message{Role: "assistant", Content: content})
		for _, call := range response.Calls {
			if c.Done {
				return errors.New("tool call after task_done")
			}
			if call.ID == "" || seen[call.ID] {
				return errors.New("duplicate tool call id")
			}
			seen[call.ID] = true
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
				return errors.New("agent reported incomplete materials")
			}
			return nil
		}
	}
	return errors.New("turn budget exhausted without task_done")
}
