package llmloop

import (
	"context"
	"encoding/json"
	"testing"

	"resume-agent-kernel/internal/agent"
	"resume-agent-kernel/internal/model"
	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/tools"
)

type scriptedClient struct {
	turns []string
	calls int
}

func (s *scriptedClient) Complete(context.Context, []model.Message) (string, model.Usage, error) {
	s.calls++
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
