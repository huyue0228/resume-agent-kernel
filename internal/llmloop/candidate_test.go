package llmloop

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"resume-agent-kernel/internal/agent"
	"resume-agent-kernel/internal/model"
	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/tools"
)

type scriptedClient struct {
	turns   []string
	calls   int
	history []model.Message
}

func TestInvalidToolResponseCorrectionIsBounded(t *testing.T) {
	for _, invalid := range []string{"not json", `{"kind":"final","tool_calls":[]}`, `{"kind":"tool_calls","tool_calls":[{"name":"task_done","arguments":"invalid"}]}`} {
		t.Run(invalid, func(t *testing.T) {
			for _, recover := range []bool{true, false} {
				c := agent.NewCollector("负责后端服务开发与测试工作", []p.JobSnapshotV1{{Ref: "j"}})
				r, _ := tools.NewProviders(&agent.Provider{Collector: c})
				client := &scriptedClient{turns: []string{invalid}}
				if recover {
					client.turns = append(client.turns, callJSON(p.ToolCall{Name: "task_done", Arguments: map[string]any{"status": "FAILED"}}))
				}
				trace := &p.SafeTrace{}
				err := RunCandidate(context.Background(), client, r, c, p.TaskBudgetV1{MaxTurns: 10, MaxToolCalls: 10, MaxTokens: 100000}, trace)
				if recover {
					if !errors.Is(err, ErrIncomplete) || !c.Done || trace.ToolCallCount != 1 || trace.Turns != 2 {
						t.Fatalf("corrected response was not executed: %v", err)
					}
				} else if !errors.Is(err, ErrInvalidOutput) || trace.Turns != 3 || trace.ToolCallCount != 0 || c.Done {
					t.Fatalf("invalid output escaped correction limit: %v %+v", err, trace)
				}
			}
		})
	}
}

func TestModelMetadataDoesNotChangeToolExecution(t *testing.T) {
	c := agent.NewCollector("负责后端服务开发与测试工作", []p.JobSnapshotV1{{Ref: "j"}})
	r, _ := tools.NewProviders(&agent.Provider{Collector: c})
	client := &scriptedClient{turns: []string{`{"kind":"tool_calls","explanation":"model metadata","action":"dispatch","tool_calls":[{"id":1,"name":"task_done","arguments":{"status":"FAILED"},"note":"model metadata"}]}`}}
	trace := &p.SafeTrace{}
	err := RunCandidate(context.Background(), client, r, c, p.TaskBudgetV1{MaxTurns: 1, MaxToolCalls: 1, MaxTokens: 100000}, trace)
	if !errors.Is(err, ErrIncomplete) || !c.Done || !c.Failed || trace.ToolCallCount != 1 {
		t.Fatalf("non-execution metadata rejected a valid tool: %v", err)
	}
	// arguments 内的额外字段仍拒绝，不能通过宽容的外层解析绕开工具校验。
	c = agent.NewCollector("负责后端服务开发与测试工作", []p.JobSnapshotV1{{Ref: "j"}})
	r, _ = tools.NewProviders(&agent.Provider{Collector: c})
	client.turns = []string{`{"kind":"tool_calls","tool_calls":[{"id":{},"name":"task_done","arguments":{"status":"FAILED","action":"dispatch"}}]}`}
	trace = &p.SafeTrace{}
	err = RunCandidate(context.Background(), client, r, c, p.TaskBudgetV1{MaxTurns: 1, MaxToolCalls: 1, MaxTokens: 100000}, trace)
	if err == nil || c.Done || trace.ToolCalls[0].Status != "error" {
		t.Fatal("tool argument validation bypassed")
	}
}

func (s *scriptedClient) Complete(_ context.Context, messages []model.Message) (string, model.Usage, error) {
	s.calls++
	s.history = append([]model.Message(nil), messages...)
	return s.turns[min(s.calls-1, len(s.turns)-1)], model.Usage{InputTokens: 10, OutputTokens: 10}, nil
}
func callJSON(calls ...p.ToolCall) string {
	raw, _ := json.Marshal(map[string]any{"kind": "tool_calls", "tool_calls": calls})
	return string(raw)
}
func TestCandidateRequiresExplicitDoneAndBudgets(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		tools         int
	}{
		{"no_done", callJSON(p.ToolCall{ID: "1", Name: "resume.list_sections", Arguments: map[string]any{}}), 4},
		{"unknown_job", callJSON(p.ToolCall{ID: "1", Name: "job.read_requirement", Arguments: map[string]any{"job_ref": "outside"}}), 4},
		{"tool_budget", callJSON(p.ToolCall{ID: "1", Name: "resume.list_sections", Arguments: map[string]any{}}, p.ToolCall{ID: "2", Name: "jobs.list_candidates", Arguments: map[string]any{}}), 1},
		{"final_action", `{"kind":"final","action":"dispatch"}`, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := agent.NewCollector("负责后端服务开发与测试工作", []p.JobSnapshotV1{{Ref: "j"}})
			r, _ := tools.NewProviders(&agent.Provider{Collector: c})
			client := &scriptedClient{turns: []string{tc.content}}
			trace := &p.SafeTrace{}
			if RunCandidate(context.Background(), client, r, c, p.TaskBudgetV1{MaxTurns: 1, MaxToolCalls: tc.tools, MaxTokens: 100000}, trace) == nil {
				t.Fatal("incomplete loop accepted")
			}
		})
	}
}
func TestSuccessfulCollectorToolLoop(t *testing.T) {
	c := agent.NewCollector("负责后端服务开发与测试工作", []p.JobSnapshotV1{{Ref: "j"}})
	r, _ := tools.NewProviders(&agent.Provider{Collector: c})
	evidence := []any{map[string]any{"quote": "负责后端服务开发与测试工作", "page": 1, "start_line": 1, "end_line": 1}}
	content := callJSON(
		p.ToolCall{ID: "p", Name: "candidate_profile.submit", Arguments: map[string]any{"claims": []any{map[string]any{"kind": "project", "summary": "服务开发", "evidence": evidence}}, "risks": []string{}}},
		p.ToolCall{ID: "m", Name: "job_match.submit", Arguments: map[string]any{"matches": []any{map[string]any{"job_ref": "j", "dimensions": map[string]any{"major_match": .8, "skills_match": .8, "experience_evidence": .8, "job_requirement": .8, "resume_quality": .8}, "confidence": .8, "reason": "项目支持", "evidence": evidence, "risks": []string{}}}}},
		p.ToolCall{ID: "d", Name: "task_done", Arguments: map[string]any{"status": "DONE"}},
	)
	trace := &p.SafeTrace{}
	err := RunCandidate(context.Background(), &scriptedClient{turns: []string{content}}, r, c, p.TaskBudgetV1{MaxTurns: 2, MaxToolCalls: 5, MaxTokens: 100000}, trace)
	if err != nil || !c.Done || len(c.Matches) != 1 {
		t.Fatalf("loop failed: %v %+v", err, trace)
	}
}

func TestToolIDsAreNormalizedAndResultsStayCorrelated(t *testing.T) {
	for _, id := range []string{"", "reused", "call_2_1"} {
		t.Run("model_id_"+id, func(t *testing.T) {
			c := agent.NewCollector("负责后端服务开发与测试工作", []p.JobSnapshotV1{{Ref: "j"}})
			r, _ := tools.NewProviders(&agent.Provider{Collector: c})
			evidence := []any{map[string]any{"quote": "负责后端服务开发与测试工作", "page": 1, "start_line": 1, "end_line": 1}}
			client := &scriptedClient{turns: []string{
				callJSON(p.ToolCall{ID: id, Name: "resume.list_sections", Arguments: map[string]any{}}),
				callJSON(
					p.ToolCall{ID: id, Name: "candidate_profile.submit", Arguments: map[string]any{"claims": []any{map[string]any{"kind": "project", "summary": "服务开发", "evidence": evidence}}, "risks": []string{}}},
					p.ToolCall{ID: id, Name: "job_match.submit", Arguments: map[string]any{"matches": []any{map[string]any{"job_ref": "j", "dimensions": map[string]any{"major_match": .8, "skills_match": .8, "experience_evidence": .8, "job_requirement": .8, "resume_quality": .8}, "confidence": .8, "reason": "项目支持", "evidence": evidence, "risks": []string{}}}}},
				),
				callJSON(p.ToolCall{ID: id, Name: "task_done", Arguments: map[string]any{"status": "DONE"}}),
			}}
			trace := &p.SafeTrace{}
			if err := RunCandidate(context.Background(), client, r, c, p.TaskBudgetV1{MaxTurns: 3, MaxToolCalls: 4, MaxTokens: 100000}, trace); err != nil || !c.Done || len(c.Matches) != 1 {
				t.Fatalf("valid tools with reused/missing IDs failed: %v", err)
			}
			pending := []string{}
			seen := map[string]bool{}
			for _, message := range client.history[2:] {
				if message.Role == "assistant" {
					var response struct {
						Calls []p.ToolCall `json:"tool_calls"`
					}
					if err := json.Unmarshal([]byte(message.Content), &response); err != nil {
						t.Fatal(err)
					}
					for _, call := range response.Calls {
						if call.ID == "" || seen[call.ID] {
							t.Fatal("runtime IDs are not unique")
						}
						seen[call.ID] = true
						pending = append(pending, call.ID)
					}
				} else {
					var result struct {
						ID string `json:"tool_call_id"`
					}
					if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
						t.Fatal(err)
					}
					if len(pending) == 0 || pending[0] != result.ID {
						t.Fatal("tool result lost its call correlation")
					}
					pending = pending[1:]
				}
			}
			if len(pending) != 0 || len(seen) != 3 || trace.ToolCallCount != 4 {
				t.Fatal("tool accounting changed")
			}
		})
	}
}
