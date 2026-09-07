package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"resume-agent-kernel/internal/protocol"
)

func validEnvelope(modelURL string) protocol.CaseEnvelopeV2 {
	return protocol.CaseEnvelopeV2{
		ProtocolVersion:  protocol.ProtocolVersion,
		TaskID:           "task-1",
		IdempotencyKey:   "idem-1",
		Pin:              protocol.Pin{PinID: "pin-1", KernelBuild: "test-build", ProtocolVersion: protocol.ProtocolVersion, ToolsetVersion: protocol.ToolsetVersion, ResultSchemaVersion: protocol.ResultVersion, PolicyVersion: "django-policy-gate/v1", InstructionVersion: protocol.InstructionVersion, ModelConfigRevision: "model-v1"},
		Constraints:      protocol.Constraints{WorkflowRevision: 2, VolunteerRank: 1, Policies: []string{"只处理当前志愿"}},
		Candidate:        protocol.CandidateReference{HighestMajor: "计算机科学"},
		CurrentVolunteer: protocol.Volunteer{PositionName: "后端工程师"},
		CurrentJob:       protocol.JobContext{PositionName: "后端工程师", Responsibilities: "开发可靠的 Go 服务", DepartmentName: "平台部"},
		Resume:           protocol.ResumeContent{Checksum: strings.Repeat("a", 64), Text: "项目经历：负责 Go 服务开发与性能优化。"},
		Model:            protocol.ModelConfig{APIStyle: "chat_json", BaseURL: modelURL, ModelName: "test-model", TimeoutSeconds: 10},
		Budget:           protocol.Budget{MaxTurns: 4, MaxToolCalls: 4, MaxDurationSeconds: 30},
	}
}

func TestKernelRunsBoundedToolLoopAndReturnsSafeTrace(t *testing.T) {
	var calls atomic.Int32
	modelServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		var content string
		if call == 1 {
			content = `{"kind":"tool_calls","tool_calls":[{"id":"read-1","name":"resume.read_sections","arguments":{"start_line":0,"max_lines":20}}],"final":null}`
		} else {
			content = `{"kind":"final","tool_calls":[],"final":{"action":"review","evaluation":{"profile":{"major_direction":"后端开发","educations":[],"projects":[],"internships":[],"skills":["Go"],"certificates":[],"summary":"具备后端经验","risk_flags":[]},"decision":{"recommendation":"review","score_breakdown":{"major_match":0.8,"skills_match":0.8,"experience_evidence":0.7,"job_requirement":0.8,"resume_quality":0.7},"summary":"建议复核","reason":"具备相关经验","evidence":["负责 Go 服务开发与性能优化"],"risks":[],"ai_specialist_match":false,"ai_specialist_confidence":0,"ai_specialist_evidence":[]}}}}`
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer modelServer.Close()

	service := NewService("test-build")
	proposal, err := service.Evaluate(context.Background(), validEnvelope(modelServer.URL), "")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Action != "review" || proposal.Trace.ToolCallCount != 1 || proposal.Trace.Turns != 2 {
		t.Fatalf("unexpected proposal: %s", mustJSON(proposal))
	}
	if calls.Load() != 2 {
		t.Fatalf("expected two model turns, got %d", calls.Load())
	}

	cached, err := service.Evaluate(context.Background(), validEnvelope(modelServer.URL), "")
	if err != nil || cached.Trace.TraceID != proposal.Trace.TraceID || calls.Load() != 2 {
		t.Fatal("idempotent evaluation did not reuse the completed lease")
	}
}

func TestKernelRejectsMismatchedPinnedBuildBeforeCallingModel(t *testing.T) {
	envelope := validEnvelope("http://model.invalid")
	envelope.Pin.KernelBuild = "another-build"

	_, err := NewService("test-build").Evaluate(context.Background(), envelope, "")
	if err == nil || !strings.Contains(err.Error(), "kernel build mismatch") {
		t.Fatalf("expected kernel build mismatch, got %v", err)
	}
}

func TestKernelFailureCarriesSafeTrace(t *testing.T) {
	modelServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"choices":[]}`))
	}))
	defer modelServer.Close()

	_, err := NewService("test-build").Evaluate(context.Background(), validEnvelope(modelServer.URL), "")
	var evaluationError *protocol.EvaluationError
	if !errors.As(err, &evaluationError) {
		t.Fatalf("expected evaluation error with trace, got %T", err)
	}
	if evaluationError.Trace.Status != "failed" || evaluationError.Trace.TraceID == "" || evaluationError.Trace.FinishedAt.IsZero() {
		t.Fatalf("unexpected failure trace: %#v", evaluationError.Trace)
	}
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("marshal error: %v", err)
	}
	return string(data)
}
