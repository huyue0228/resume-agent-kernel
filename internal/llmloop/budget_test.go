package llmloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"resume-agent-kernel/internal/agent"
	"resume-agent-kernel/internal/model"
	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/tools"
)

type measuredClient struct {
	t     *testing.T
	turn  int
	reply func(int, []model.Message) string
}

func (s *measuredClient) Complete(_ context.Context, messages []model.Message) (string, model.Usage, error) {
	s.turn++
	content := s.reply(s.turn, messages)
	counter, err := model.NewTokenCounter()
	if err != nil {
		s.t.Fatal(err)
	}
	return content, model.Usage{InputTokens: counter.Base(messages), OutputTokens: counter.Text(content), InputReported: true, OutputReported: true}, nil
}
func loopFixture(t *testing.T, text string) (*agent.Collector, *tools.Registry) {
	t.Helper()
	c := agent.NewCollector(text, []p.JobSnapshotV1{{Ref: "j"}})
	r, err := tools.NewProviders(&agent.Provider{Collector: c})
	if err != nil {
		t.Fatal(err)
	}
	return c, r
}
func successfulCalls(quote string) []p.ToolCall {
	evidence := []any{map[string]any{"quote": quote, "page": 1, "start_line": 1, "end_line": 1}}
	return []p.ToolCall{
		{Name: "candidate_profile.submit", Arguments: map[string]any{"claims": []any{map[string]any{"kind": "project", "summary": "项目开发", "evidence": evidence}}, "risks": []string{}}},
		{Name: "job_match.submit", Arguments: map[string]any{"matches": []any{map[string]any{"job_ref": "j", "dimensions": map[string]any{"major_match": .8, "skills_match": .8, "experience_evidence": .8, "job_requirement": .8, "resume_quality": .8}, "confidence": .8, "reason": "原文证据支持", "evidence": evidence, "risks": []string{}}}}},
		{Name: "task_done", Arguments: map[string]any{"status": "DONE"}},
	}
}
func Test96338TokensCanStillFinishWithChineseContext(t *testing.T) {
	quote := "负责后端服务开发与测试工作"
	c, r := loopFixture(t, quote)
	for i := 0; i < 20; i++ {
		c.TagCatalog = append(c.TagCatalog, p.AbilityTagV1{Code: fmt.Sprintf("tag%d", i), Name: "软件开发", Description: strings.Repeat("负责开发", 80)})
	}
	b := p.TaskBudgetV1{MaxTokens: 120000, MaxContextTokens: 32768, MaxTurns: 32, MaxToolCalls: 256}
	trace := &p.SafeTrace{Turns: 15, InputTokens: 90000, OutputTokens: 6338}
	client := &measuredClient{t: t, reply: func(_ int, messages []model.Message) string {
		bytes := 0
		for _, m := range messages {
			bytes += len(m.Content) + 16
		}
		if bytes <= 23662 {
			t.Fatal("fixture no longer reproduces the former byte-based rejection")
		}
		if !strings.Contains(messages[0].Content, "收尾阶段") {
			t.Fatal("remaining budget must trigger finalization")
		}
		return callJSON(successfulCalls(quote)...)
	}}
	if err := RunCandidate(context.Background(), client, r, c, b, trace); err != nil {
		t.Fatal(err)
	}
	if !c.Done || trace.InputTokens+trace.OutputTokens > 120000 || client.turn != 1 {
		t.Fatalf("did not complete within original budget: %+v", trace.Budget)
	}
	if trace.Rounds[0].Phase != "finalize" || trace.Rounds[0].UsageSource != "reported" {
		t.Fatal("missing honest round diagnostics")
	}
}
func TestRepeatedNonemptyValidationErrorsStopBeforeBudget(t *testing.T) {
	c, r := loopFixture(t, "负责后端服务开发与测试工作")
	calls := successfulCalls("这段引用并不存在于简历原文")
	client := &measuredClient{t: t, reply: func(turn int, messages []model.Message) string {
		if turn > 1 && !strings.Contains(messages[len(messages)-1].Content, "evidence_mismatch") {
			t.Fatal("specific validation feedback lost")
		}
		return callJSON(calls[0])
	}}
	trace := &p.SafeTrace{}
	err := RunCandidate(context.Background(), client, r, c, p.TaskBudgetV1{MaxTokens: 120000, MaxTurns: 32, MaxToolCalls: 256}, trace)
	var stopped *StopError
	if !errors.As(err, &stopped) || stopped.Reason != "no_progress" || client.turn != 3 {
		t.Fatalf("repeated errors were not bounded: %v (%d)", err, client.turn)
	}
	if trace.Budget.RepeatedCalls != 2 || trace.Budget.ValidationFailures != 3 || trace.ToolCalls[0].ErrorField != "claims[0].evidence[0].quote" {
		t.Fatal("missing validation/repetition diagnostics")
	}
	raw, _ := json.Marshal(trace)
	if strings.Contains(string(raw), "这段引用") || strings.Contains(string(raw), "负责后端") {
		t.Fatal("resume text leaked into safe trace")
	}
}
func TestTargetedCorrectionRetainsStrictEvidenceValidation(t *testing.T) {
	quote := "负责后端服务开发与测试工作"
	c, r := loopFixture(t, quote)
	client := &measuredClient{t: t, reply: func(turn int, messages []model.Message) string {
		if turn == 1 {
			return callJSON(successfulCalls("原文不存在这段伪造证据")[0])
		}
		if c.Profile != nil || !strings.Contains(messages[len(messages)-1].Content, "claims[0].evidence[0].quote") {
			t.Fatal("invalid claim accepted or targeted feedback absent")
		}
		return callJSON(successfulCalls(quote)...)
	}}
	trace := &p.SafeTrace{}
	if err := RunCandidate(context.Background(), client, r, c, p.TaskBudgetV1{MaxTokens: 120000, MaxTurns: 32, MaxToolCalls: 256}, trace); err != nil {
		t.Fatal(err)
	}
	if trace.Budget.ValidationFailures != 1 || trace.Turns != 2 || !c.Verify(c.Profile.Claims[0].Evidence[0]) {
		t.Fatal("correction did not preserve evidence requirements")
	}
}
func TestCompactionKeepsVerifiedStateAndCanonicalLines(t *testing.T) {
	quote := "负责后端服务开发与测试工作"
	text := quote + "\n" + strings.Repeat("项目实施与服务性能优化，所有证据保留原文行号。\n", 500)
	c, r := loopFixture(t, text)
	calls := successfulCalls(quote)
	client := &measuredClient{t: t, reply: func(turn int, messages []model.Message) string {
		switch turn {
		case 1, 2, 3:
			return callJSON(p.ToolCall{Name: "resume.read_sections", Arguments: map[string]any{"start_line": (turn-1)*100 + 1, "end_line": turn * 100}})
		case 4:
			return callJSON(calls[0])
		case 5:
			if !strings.Contains(messages[1].Content, `"verified_profile":{"claims"`) && !strings.Contains(messages[1].Content, `"verified_profile":{"source_text"`) {
				t.Fatal("verified profile missing after history compaction")
			}
			return callJSON(calls[1:]...)
		default:
			t.Fatal("unexpected extra round")
			return ""
		}
	}}
	trace := &p.SafeTrace{}
	if err := RunCandidate(context.Background(), client, r, c, p.TaskBudgetV1{MaxTokens: 250000, MaxContextTokens: 22000, MaxTurns: 32, MaxToolCalls: 256}, trace); err != nil {
		t.Fatal(err)
	}
	if trace.Budget.Compactions == 0 || len(c.Lines) != 502 || c.Lines[0].Text != quote || !c.Verify(c.Profile.Claims[0].Evidence[0]) {
		t.Fatalf("compaction lost evidence or never ran: %+v", trace.Budget)
	}
}
func TestContextAndTotalBudgetStopsAreDistinct(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget p.TaskBudgetV1
		want   string
	}{
		{"context", p.TaskBudgetV1{MaxTokens: 120000, MaxContextTokens: 1024, MaxTurns: 32, MaxToolCalls: 256}, "context_limit"},
		{"total", p.TaskBudgetV1{MaxTokens: 500, MaxContextTokens: 32768, MaxTurns: 32, MaxToolCalls: 256}, "next_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, r := loopFixture(t, "负责后端服务开发与测试工作")
			client := &scriptedClient{}
			trace := &p.SafeTrace{}
			err := RunCandidate(context.Background(), client, r, c, tc.budget, trace)
			var stopped *StopError
			if !errors.As(err, &stopped) || stopped.Reason != tc.want || client.calls != 0 || trace.Budget.NextInputTokens == 0 {
				t.Fatalf("unexpected preflight: %v", err)
			}
		})
	}
}
func TestFinalizationDoesNotExecuteExplorationTools(t *testing.T) {
	c, r := loopFixture(t, "负责后端服务开发与测试工作")
	client := &scriptedClient{turns: []string{callJSON(p.ToolCall{Name: "resume.search_evidence", Arguments: map[string]any{"query": "开发"}})}}
	trace := &p.SafeTrace{}
	_ = RunCandidate(context.Background(), client, r, c, p.TaskBudgetV1{MaxTokens: 100000, MaxTurns: 1, MaxToolCalls: 256}, trace)
	if trace.Turns != 1 || trace.ToolCalls[0].ErrorCode != "finalize_only" || trace.ToolCalls[0].Status != "error" {
		t.Fatal("finalization escaped tool or round boundary")
	}
}

func TestRereadingCompactedEvidenceIsProgress(t *testing.T) {
	c, r := loopFixture(t, "负责后端服务开发与测试工作")
	h := newConversation()
	call := p.ToolCall{Name: "resume.read_sections", Arguments: map[string]any{"start_line": 1, "end_line": 1}}
	raw, _, err := r.Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if advanced, _ := h.observe(call, raw, nil, 0, c); !advanced {
		t.Fatal("first read did not advance")
	}
	msg, _ := json.Marshal(map[string]any{"result": json.RawMessage(raw)})
	h.rounds = [][]model.Message{{{Role: "user", Content: string(msg)}}}
	if advanced, repeated := h.observe(call, raw, nil, 0, c); advanced || !repeated {
		t.Fatal("resident repeat misclassified")
	}
	h.rounds = append(h.rounds, []model.Message{{Role: "user", Content: `{"result":{}}`}})
	h.compact(1)
	if advanced, _ := h.observe(call, raw, nil, 0, c); !advanced {
		t.Fatal("legitimate evidence reload was treated as a stall")
	}
}

func TestUnknownTransportUsageIsEstimatedAndReserved(t *testing.T) {
	counter, err := model.NewTokenCounter()
	if err != nil {
		t.Fatal(err)
	}
	messages := []model.Message{{Role: "user", Content: "synthetic"}}
	usage := model.Usage{Attempts: 2, UnknownInputAttempts: 2, UnknownOutputAttempts: 2}
	in, out, source := recordUsage(counter, messages, "", usage, 4096)
	if in != 2*counter.Estimate(messages) || out != 8192 || source != "estimated" {
		t.Fatal("unknown retries appeared free")
	}
}

func TestFinalSubmissionRetainsBudgetForEvidenceCorrection(t *testing.T) {
	quote := "负责后端服务开发与测试工作"
	c, r := loopFixture(t, quote)
	client := &measuredClient{t: t, reply: func(turn int, messages []model.Message) string {
		if turn == 1 {
			return callJSON(successfulCalls("原文不存在这段伪造证据")[0])
		}
		if !strings.Contains(messages[len(messages)-1].Content, "evidence_mismatch") {
			t.Fatal("final correction lost validation feedback")
		}
		return callJSON(successfulCalls(quote)...)
	}}
	trace := &p.SafeTrace{}
	err := RunCandidate(context.Background(), client, r, c, p.TaskBudgetV1{MaxTokens: 80000, MaxTurns: 2, MaxToolCalls: 256}, trace)
	if err != nil || !c.Done || trace.Turns != 2 || trace.Rounds[0].ReservedTokens == 0 || trace.Rounds[1].ReservedTokens != 0 {
		t.Fatalf("final correction was not budgeted within the turn limit: %v", err)
	}
}
