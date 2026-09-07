package protocol

import (
	"strings"
	"testing"
)

func TestEnvelopeValidateRejectsIncompatibleToolset(t *testing.T) {
	envelope := CaseEnvelopeV2{
		ProtocolVersion:  ProtocolVersion,
		TaskID:           "task-1",
		IdempotencyKey:   "idem-1",
		Pin:              Pin{PinID: "pin", KernelBuild: "build", ProtocolVersion: ProtocolVersion, ToolsetVersion: "unexpected", ResultSchemaVersion: ResultVersion, PolicyVersion: PolicyVersion, InstructionVersion: InstructionVersion, ModelConfigRevision: "revision"},
		Constraints:      Constraints{VolunteerRank: 1, Policies: []string{"固定当前志愿"}},
		CurrentVolunteer: Volunteer{PositionName: "后端工程师"},
		CurrentJob:       JobContext{PositionName: "后端工程师", Responsibilities: "开发可靠服务"},
		Resume:           ResumeContent{Checksum: strings.Repeat("a", 64), Text: "候选人有后端开发经历"},
		Model:            ModelConfig{APIStyle: "chat_json", BaseURL: "http://model.local/v1", ModelName: "test", TimeoutSeconds: 10},
		Budget:           Budget{MaxTurns: 2, MaxToolCalls: 2, MaxDurationSeconds: 30},
	}
	if err := envelope.Validate(); err == nil {
		t.Fatal("expected incompatible toolset to be rejected")
	}
}

func TestScreeningOutputRejectsOutOfRangeScore(t *testing.T) {
	output := ScreeningOutput{Decision: Decision{Recommendation: "review", ScoreBreakdown: ScoreBreakdown{MajorMatch: 1.1}}}
	if err := output.Validate(); err == nil {
		t.Fatal("expected score validation error")
	}
}
