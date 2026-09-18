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
	b = initBudget(b, trace)
	modelName := ""
	if named, ok := client.(interface{ ModelName() string }); ok {
		modelName = named.ModelName()
	}
	counter, err := model.NewTokenCounterForModel(modelName)
	if err != nil {
		return err
	}
	trace.Budget.Tokenizer = counter.Name()
	history := newConversation()
	final := false
	finalAttempted := false
	allowedNames := map[string]bool{}
	for _, definition := range registry.Catalog() {
		allowedNames[definition.Name] = true
	}
	attempts := 1
	if httpClient, ok := client.(*model.HTTPClient); ok {
		attempts = httpClient.MaxAttempts()
	}
	for trace.Turns < b.MaxTurns {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := b.MaxTokens - trace.InputTokens - trace.OutputTokens
		trace.Budget.RemainingTokens = max(0, remaining)
		if remaining <= 0 {
			return stop(trace, "token_limit")
		}
		if trace.ToolCallCount >= b.MaxToolCalls {
			return stop(trace, "tool_limit")
		}
		if history.noProgress >= 3 {
			return stop(trace, "no_progress")
		}
		final = final || b.MaxTurns-trace.Turns <= 2 || b.MaxToolCalls-trace.ToolCallCount <= 3 || history.noProgress >= 2
		if deadline, ok := ctx.Deadline(); ok && b.MaxDurationSeconds > 0 && time.Until(deadline) < time.Duration(min(15, max(1, b.MaxDurationSeconds/10)))*time.Second {
			final = true
		}
		messages := history.messages(registry, c, b, trace, final)
		estimatedInput := counter.Estimate(messages)
		compacted := false
		if estimatedInput > b.MaxContextTokens*3/5 || estimatedInput*3 > remaining {
			compacted = history.compact(2)
			messages = history.messages(registry, c, b, trace, final)
			estimatedInput = counter.Estimate(messages)
		}
		// Reserve a complete final submission and one bounded correction inside
		// the declared total, including their input replay and transport retries.
		finalMessages := history.messages(registry, c, b, trace, true)
		reserve := 2 * (counter.Estimate(finalMessages) + 4096) * attempts
		if !final && (remaining < reserve+(estimatedInput+1024)*attempts || estimatedInput+2048 > b.MaxContextTokens) {
			final = true
			if history.compact(2) {
				compacted = true
			}
			messages = history.messages(registry, c, b, trace, true)
			estimatedInput = counter.Estimate(messages)
		}
		if final {
			reserve = 0
			// Keep one correction affordable after the first final submission,
			// when the remaining token and turn budgets can support both calls.
			if !finalAttempted && b.MaxTurns-trace.Turns >= 2 && remaining >= 2*(estimatedInput+1024)*attempts {
				reserve = min((estimatedInput+4096)*attempts, remaining-(estimatedInput+1024)*attempts)
			}
		}
		available := min((remaining-reserve)/attempts-estimatedInput, b.MaxContextTokens-estimatedInput)
		if available < 1024 && history.compact(1) {
			compacted = true
			messages = history.messages(registry, c, b, trace, final)
			estimatedInput = counter.Estimate(messages)
			available = min((remaining-reserve)/attempts-estimatedInput, b.MaxContextTokens-estimatedInput)
		}
		trace.Budget.NextInputTokens = estimatedInput
		trace.Budget.ReservedTokens = reserve
		if compacted {
			trace.Budget.Compactions++
		}
		// Once only completion remains, a small output is sufficient. Otherwise
		// do not knowingly truncate a structured submission to a tiny response.
		minimumOutput := 1024
		if c.Profile != nil && len(c.Matches) == len(c.Jobs) {
			minimumOutput = 128
		}
		if available < minimumOutput {
			if b.MaxContextTokens-estimatedInput < minimumOutput {
				return stop(trace, "context_limit")
			}
			return stop(trace, "next_request")
		}
		outputLimit := min(available, 8192)
		if httpClient, ok := client.(*model.HTTPClient); ok {
			httpClient.OutputBudget = outputLimit
		}
		started := time.Now()
		content, usage, callErr := client.Complete(ctx, messages)
		modelDuration := time.Since(started).Milliseconds()
		finalAttempted = finalAttempted || final
		input, output, usageSource := recordUsage(counter, messages, content, usage, outputLimit)
		trace.Turns++
		trace.InputTokens += input
		trace.OutputTokens += output
		trace.Budget.RemainingTokens = max(0, b.MaxTokens-trace.InputTokens-trace.OutputTokens)
		trace.Budget.TransportRetries += max(0, usage.Attempts-1)
		trace.Rounds = append(trace.Rounds, p.RoundTrace{Turn: trace.Turns, Phase: phaseName(final), EstimatedInputTokens: estimatedInput, InputTokens: input, OutputTokens: output, OutputLimit: outputLimit, RemainingTokens: trace.Budget.RemainingTokens, ReservedTokens: reserve, ModelDurationMS: modelDuration, UsageSource: usageSource, TransportAttempts: max(1, usage.Attempts), Compacted: compacted})
		trace.Budget.UsageSource = mergeUsageSource(trace.Rounds)
		if trace.InputTokens+trace.OutputTokens > b.MaxTokens {
			return stop(trace, "token_limit")
		}
		if callErr != nil && !errors.Is(callErr, model.ErrInvalidResponse) {
			trace.Budget.StopReason = "model_error"
			return callErr
		}
		response, parseErr := parseToolResponse(content)
		if callErr != nil || parseErr != nil {
			if trace.Budget.FormatRepairs >= 2 || trace.Turns >= b.MaxTurns {
				trace.Budget.StopReason = "invalid_output"
				return ErrInvalidOutput
			}
			trace.Budget.FormatRepairs++
			history.noProgress++
			history.rounds = append(history.rounds, []model.Message{{Role: "user", Content: `{"error":"本轮未执行任何工具。仅返回 kind=tool_calls 的 JSON，tool_calls 为非空数组，每项包含 name、arguments。按 runtime_state 继续，不要重复成功提交。"}`}})
			continue
		}
		for i := range response.Calls {
			response.Calls[i].ID = fmt.Sprintf("call_%d_%d", trace.Turns, i+1)
		}
		normalized, _ := json.Marshal(response)
		round := []model.Message{{Role: "assistant", Content: string(normalized)}}
		progress := false
		for _, call := range response.Calls {
			if c.Done {
				trace.Budget.StopReason = "invalid_output"
				return ErrInvalidOutput
			}
			if trace.ToolCallCount >= b.MaxToolCalls {
				return stop(trace, "tool_limit")
			}
			trace.ToolCallCount++
			started := time.Now()
			before := collectorProgress(c)
			var raw json.RawMessage
			var count int
			var toolErr error
			if final && !finalTool(call.Name) {
				toolErr = tools.Invalid("finalize_only", "name", "当前只允许 available_tools 中的收尾工具；提交已取得的证据和分析或以 FAILED 结束。")
			} else {
				raw, count, toolErr = registry.Execute(ctx, call)
			}
			advanced, repeated := history.observe(call, raw, toolErr, before, c)
			progress = progress || advanced
			if repeated {
				trace.Budget.RepeatedCalls++
			}
			status, code, field := "ok", "", ""
			if toolErr != nil {
				if errors.Is(toolErr, context.Canceled) || errors.Is(toolErr, context.DeadlineExceeded) {
					return toolErr
				}
				status = "error"
				feedback := tools.Feedback(toolErr)
				code, field = feedback.Code, feedback.Field
				trace.Budget.ValidationFailures++
				raw, _ = json.Marshal(map[string]any{"error": feedback, "repeat_unchanged": repeated})
			}
			traceName := call.Name
			if !allowedNames[traceName] {
				traceName = "unregistered_tool"
			}
			trace.ToolCalls = append(trace.ToolCalls, p.ToolTrace{Name: traceName, Status: status, DurationMS: time.Since(started).Milliseconds(), ItemCount: count, ErrorCode: code, ErrorField: field, Repeated: repeated})
			if len(raw) == 0 {
				raw = json.RawMessage(`null`)
			}
			msg, _ := json.Marshal(map[string]any{"tool_call_id": call.ID, "result": raw})
			round = append(round, model.Message{Role: "user", Content: string(msg)})
		}
		history.rounds = append(history.rounds, round)
		trace.Rounds[len(trace.Rounds)-1].Progress = progress
		if progress {
			history.noProgress = 0
		} else {
			history.noProgress++
		}
		if c.Done {
			if c.Failed {
				trace.Budget.StopReason = "materials_incomplete"
				return ErrIncomplete
			}
			return nil
		}
	}
	return stop(trace, "turn_limit")
}
