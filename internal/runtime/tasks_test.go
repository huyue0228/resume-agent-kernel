package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"resume-agent-kernel/internal/contract"
	"resume-agent-kernel/internal/pipeline"
	p "resume-agent-kernel/internal/protocol"
	httpserver "resume-agent-kernel/internal/server"
)

type testDocument struct{ calls int }

func (d *testDocument) Read(context.Context, p.ArtifactRefV1, int) (pipeline.Document, error) {
	d.calls++
	return pipeline.Document{Text: strings.Repeat("负责后端服务开发与测试工作。", 20), Checksum: strings.Repeat("a", 64)}, nil
}

func TestTaskFailuresKeepSafeSpecificCodes(t *testing.T) {
	for _, tc := range []struct {
		name, content, code string
		status              int
		delay               time.Duration
	}{
		{name: "invalid_json", content: "not json", code: "model_output_invalid"},
		{name: "missing_done", content: `{"kind":"final"}`, code: "model_output_invalid"},
		{name: "turn_budget", content: `{"kind":"tool_calls","tool_calls":[{"id":"","name":"resume.list_sections","arguments":{}}]}`, code: "budget_exhausted"},
		{name: "incomplete", content: `{"kind":"tool_calls","tool_calls":[{"name":"task_done","arguments":{"status":"FAILED"}}]}`, code: "materials_incomplete"},
		{name: "connection", status: 401, code: "model_connection_error"},
		{name: "rate_limit", status: 429, code: "model_rate_limited"},
		{name: "timeout", delay: 50 * time.Millisecond, code: "task_timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(tc.delay)
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					w.Write([]byte("private response body"))
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": tc.content}}}})
			}))
			defer server.Close()
			e := taskEnvelope()
			e.Budget.MaxTurns = 1
			e.Model = p.ModelConfig{APIStyle: "chat_json", BaseURL: server.URL, ModelName: "fake", TimeoutSeconds: 1}
			if tc.delay > 0 {
				e.Model.TimeoutSeconds = .01
			}
			s := NewService("test")
			s.Documents = &testDocument{}
			result, err := s.Execute(context.Background(), e, "unit-test-only")
			if err != nil || result.Manifest.TerminalState != "FAILED" || result.Manifest.FailureCode != tc.code {
				t.Fatalf("failure classification: %s, %v", result.Manifest.FailureCode, err)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "unit-test-only") || strings.Contains(string(raw), "private response body") {
				t.Fatal("failure leaked sensitive data")
			}
		})
	}
}
func taskEnvelope() p.TaskEnvelopeV1 {
	capabilities, _ := NewService("test").Capabilities()
	return p.TaskEnvelopeV1{ProtocolVersion: p.TaskProtocolVersion, TaskKind: p.ResumeJobMatchTaskKind, TaskID: "task", IdempotencyKey: "key", Pin: p.TaskPinV1{PinID: "pin", KernelBuild: "test", ProtocolVersion: p.TaskProtocolVersion, ToolsetVersion: capabilities.ToolsetVersion, ResultSchemaVersion: p.TaskResultVersion, PolicyVersion: "platform-policy/v99", InstructionVersion: capabilities.InstructionVersion, ModelConfigRevision: "model"}, Snapshot: p.CaseSnapshotV1{Candidate: p.CandidateSnapshotV1{Ref: "candidate"}, Volunteers: []p.VolunteerSnapshotV1{{Ref: "v", Entity: "GW", PositionName: "开发"}}, Jobs: []p.JobSnapshotV1{{Ref: "j", ContentHash: strings.Repeat("a", 64), Entity: "GW", PublicName: "开发", PositionName: "内部开发", DepartmentRef: "d"}}}, Budget: p.TaskBudgetV1{MaxTurns: 5, MaxToolCalls: 20, MaxTokens: 100000, MaxOCRPages: 10, MaxDurationSeconds: 30}}
}
func TestEmptyAnalysisScopeNeverReadsDocumentOrModel(t *testing.T) {
	e := taskEnvelope()
	e.Snapshot.Jobs = nil
	s := NewService("test")
	documents := &testDocument{}
	s.Documents = documents
	result, err := s.Execute(context.Background(), e, "")
	if err != nil || result.Manifest.TerminalState != "FAILED" || result.Trace.Turns != 0 || documents.calls != 0 {
		t.Fatalf("bad block %+v %v", result, err)
	}
}
func TestTaskHTTPModelUsesCollectorsAndReturnsNoBusinessAction(t *testing.T) {
	evidence := map[string]any{"quote": "负责后端服务开发与测试工作", "page": 1, "start_line": 1, "end_line": 1}
	turn := map[string]any{"kind": "tool_calls", "tool_calls": []any{
		map[string]any{"id": "p", "name": "candidate_profile.submit", "arguments": map[string]any{"claims": []any{map[string]any{"kind": "project", "summary": "后端开发", "evidence": []any{evidence}}}, "risks": []string{}}},
		map[string]any{"id": "j", "name": "job_match.submit", "arguments": map[string]any{"matches": []any{map[string]any{"job_ref": "j", "dimensions": map[string]any{"major_match": .8, "skills_match": .8, "experience_evidence": .8, "job_requirement": .8, "resume_quality": .8}, "confidence": .8, "evidence": []any{evidence}, "reason": "支持岗位", "risks": []string{}}}}},
		map[string]any{"id": "d", "name": "task_done", "arguments": map[string]any{"status": "DONE"}},
	}}
	raw, _ := json.Marshal(turn)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(raw)}}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 100}})
	}))
	defer server.Close()
	e := taskEnvelope()
	e.Model = p.ModelConfig{APIStyle: "chat_json", BaseURL: server.URL, ModelName: "fake", TimeoutSeconds: 10}
	s := NewService("test")
	s.Documents = &testDocument{}
	result, err := s.Execute(context.Background(), e, "test-secret")
	if err != nil || result.Manifest.TerminalState != "DONE" || len(result.Matches) != 1 || result.Matches[0].Score != .8 {
		t.Fatalf("bad task %+v %v", result, err)
	}
	wire, _ := json.Marshal(result)
	if strings.Contains(string(wire), "test-secret") || strings.Contains(string(wire), `"action"`) || strings.Contains(string(wire), `"recommendation"`) {
		t.Fatal("business action or secret leaked")
	}
	s.Execute(context.Background(), e, "test-secret")
	if calls != 1 {
		t.Fatal("idempotency lost")
	}
	// 真正经过公开 HTTP 边界，验证共享 Schema 与运行时返回的匹配结果。
	requestData, _ := contract.Bundle.ReadFile("bundle/request.example.json")
	var request p.AnalysisRequestV1
	json.Unmarshal(requestData, &request)
	request.Model = e.Model
	request.Pin = e.Pin
	request.Scope.Jobs = []p.AnalysisJobV1{{Ref: "j", ContentHash: strings.Repeat("a", 64), RequiredMajors: []string{}}}
	wireRequest, _ := json.Marshal(request)
	kernelHTTP := httptest.NewServer(httpserver.New(s, "kernel-token", nil))
	defer kernelHTTP.Close()
	httpRequest, _ := http.NewRequest(http.MethodPost, kernelHTTP.URL+"/v2/tasks/execute", bytes.NewReader(wireRequest))
	httpRequest.Header.Set("X-Agent-Kernel-Token", "kernel-token")
	response, httpErr := http.DefaultClient.Do(httpRequest)
	if httpErr != nil {
		t.Fatal(httpErr)
	}
	responseData, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("public analysis failed: %s", responseData)
	}
	if err := contract.Validate("response", responseData); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(responseData), "deterministic") || strings.Contains(string(responseData), "recommendation") {
		t.Fatal("platform policy crossed response boundary")
	}
	e.Snapshot.Candidate.HighestMajor = "changed"
	if _, err := s.Execute(context.Background(), e, ""); err == nil {
		t.Fatal("conflicting idempotency request accepted")
	}
}
