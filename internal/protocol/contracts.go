package protocol

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ProtocolVersion    = "resume-agent/v2"
	ProposalVersion    = "agent-action-proposal/v1"
	ToolsetVersion     = "resume-tool-providers/v2"
	ResultVersion      = "resume-screening/v1"
	PolicyVersion      = "django-policy-gate/v1"
	InstructionVersion = "resume-screening-kernel/v1"
)

type Pin struct {
	PinID               string `json:"pin_id"`
	KernelBuild         string `json:"kernel_build"`
	ProtocolVersion     string `json:"protocol_version"`
	ToolsetVersion      string `json:"toolset_version"`
	ResultSchemaVersion string `json:"result_schema_version"`
	PolicyVersion       string `json:"policy_version"`
	InstructionVersion  string `json:"instruction_version"`
	ModelConfigRevision string `json:"model_config_revision"`
}

type Constraints struct {
	WorkflowRevision int64    `json:"workflow_revision"`
	VolunteerRank    int      `json:"volunteer_rank"`
	Policies         []string `json:"policies"`
}

type CandidateReference struct {
	HighestMajor string `json:"highest_major"`
}

type Volunteer struct {
	PositionName string `json:"position_name"`
}

type JobContext struct {
	Entity           string   `json:"entity"`
	PublicName       string   `json:"public_name"`
	PositionName     string   `json:"position_name"`
	Category         string   `json:"category"`
	JobFamily        string   `json:"job_family"`
	Location         string   `json:"location"`
	RequiredMajors   []string `json:"required_majors"`
	Responsibilities string   `json:"responsibilities"`
	DepartmentName   string   `json:"department_name"`
}

type ResumeContent struct {
	Checksum string `json:"checksum"`
	Text     string `json:"text"`
}

type ModelConfig struct {
	APIStyle           string  `json:"api_style"`
	BaseURL            string  `json:"base_url"`
	ModelName          string  `json:"model_name"`
	StructuredOutput   string  `json:"structured_output_mode"`
	TimeoutSeconds     float64 `json:"timeout_seconds"`
	RetryCount         int     `json:"retry_count"`
	InsecureSkipVerify bool    `json:"insecure_skip_verify"`
}

type Budget struct {
	MaxTurns           int `json:"max_turns"`
	MaxToolCalls       int `json:"max_tool_calls"`
	MaxDurationSeconds int `json:"max_duration_seconds"`
}

type CaseEnvelopeV2 struct {
	ProtocolVersion  string             `json:"protocol_version"`
	TaskID           string             `json:"task_id"`
	IdempotencyKey   string             `json:"idempotency_key"`
	Pin              Pin                `json:"pin"`
	Constraints      Constraints        `json:"constraints"`
	Candidate        CandidateReference `json:"candidate_reference"`
	CurrentVolunteer Volunteer          `json:"current_volunteer"`
	CurrentJob       JobContext         `json:"current_job"`
	Resume           ResumeContent      `json:"resume"`
	Model            ModelConfig        `json:"model"`
	Budget           Budget             `json:"budget"`
}

func (e CaseEnvelopeV2) Validate() error {
	if e.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported protocol_version %q", e.ProtocolVersion)
	}
	if strings.TrimSpace(e.TaskID) == "" || strings.TrimSpace(e.IdempotencyKey) == "" {
		return errors.New("task_id and idempotency_key are required")
	}
	if e.Pin.ProtocolVersion != ProtocolVersion || e.Pin.ToolsetVersion != ToolsetVersion || e.Pin.ResultSchemaVersion != ResultVersion || e.Pin.PolicyVersion != PolicyVersion {
		return errors.New("pinned protocol, toolset, result schema, or policy is incompatible")
	}
	if strings.TrimSpace(e.Pin.PinID) == "" || strings.TrimSpace(e.Pin.KernelBuild) == "" || e.Pin.InstructionVersion != InstructionVersion || strings.TrimSpace(e.Pin.ModelConfigRevision) == "" {
		return errors.New("pin_id, kernel_build, compatible instruction_version, and model_config_revision are required")
	}
	if e.Constraints.WorkflowRevision < 0 || e.Constraints.VolunteerRank < 1 || e.Constraints.VolunteerRank > 4 || len(e.Constraints.Policies) == 0 {
		return errors.New("workflow_revision, volunteer_rank, and policies are invalid")
	}
	if len(e.Resume.Checksum) != 64 {
		return errors.New("resume checksum must be a 64-character SHA-256")
	}
	if _, err := hex.DecodeString(e.Resume.Checksum); err != nil {
		return errors.New("resume checksum must be hexadecimal")
	}
	if strings.TrimSpace(e.Resume.Text) == "" {
		return errors.New("resume checksum and text are required")
	}
	if len(e.Resume.Text) > 60_000 {
		return errors.New("resume text exceeds 60000 characters")
	}
	if strings.TrimSpace(e.CurrentJob.Responsibilities) == "" {
		return errors.New("current job responsibilities are required")
	}
	if strings.TrimSpace(e.CurrentVolunteer.PositionName) == "" || strings.TrimSpace(e.CurrentJob.PositionName) == "" {
		return errors.New("current volunteer and current job are required")
	}
	if e.Model.APIStyle != "chat_json" && e.Model.APIStyle != "responses" {
		return errors.New("unsupported model api_style")
	}
	if strings.TrimSpace(e.Model.BaseURL) == "" || strings.TrimSpace(e.Model.ModelName) == "" {
		return errors.New("model base_url and model_name are required")
	}
	if e.Model.TimeoutSeconds <= 0 || e.Model.TimeoutSeconds > 1800 {
		return errors.New("timeout_seconds must be in (0, 1800]")
	}
	if e.Model.RetryCount < 0 || e.Model.RetryCount > 5 {
		return errors.New("retry_count must be between 0 and 5")
	}
	if e.Budget.MaxTurns < 1 || e.Budget.MaxTurns > 8 {
		return errors.New("max_turns must be between 1 and 8")
	}
	if e.Budget.MaxToolCalls < 1 || e.Budget.MaxToolCalls > 16 {
		return errors.New("max_tool_calls must be between 1 and 16")
	}
	if e.Budget.MaxDurationSeconds < 1 || e.Budget.MaxDurationSeconds > 1800 {
		return errors.New("max_duration_seconds must be between 1 and 1800")
	}
	return nil
}

type ExperienceItem struct {
	Name        string `json:"name"`
	Role        string `json:"role"`
	Period      string `json:"period"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
}

type EducationItem struct {
	SchoolName string `json:"school_name"`
	Degree     string `json:"degree"`
	Major      string `json:"major"`
	Period     string `json:"period"`
	Evidence   string `json:"evidence"`
}

type ScoreBreakdown struct {
	MajorMatch         float64 `json:"major_match"`
	SkillsMatch        float64 `json:"skills_match"`
	ExperienceEvidence float64 `json:"experience_evidence"`
	JobRequirement     float64 `json:"job_requirement"`
	ResumeQuality      float64 `json:"resume_quality"`
}

func (s ScoreBreakdown) Validate() error {
	values := []float64{s.MajorMatch, s.SkillsMatch, s.ExperienceEvidence, s.JobRequirement, s.ResumeQuality}
	for _, value := range values {
		if value < 0 || value > 1 {
			return errors.New("score breakdown values must be between 0 and 1")
		}
	}
	return nil
}

type ResumeProfile struct {
	MajorDirection string           `json:"major_direction"`
	Educations     []EducationItem  `json:"educations"`
	Projects       []ExperienceItem `json:"projects"`
	Internships    []ExperienceItem `json:"internships"`
	Skills         []string         `json:"skills"`
	Certificates   []string         `json:"certificates"`
	Summary        string           `json:"summary"`
	RiskFlags      []string         `json:"risk_flags"`
}

type Decision struct {
	Recommendation         string         `json:"recommendation"`
	ScoreBreakdown         ScoreBreakdown `json:"score_breakdown"`
	Summary                string         `json:"summary"`
	Reason                 string         `json:"reason"`
	Evidence               []string       `json:"evidence"`
	Risks                  []string       `json:"risks"`
	AISpecialistMatch      bool           `json:"ai_specialist_match"`
	AISpecialistConfidence float64        `json:"ai_specialist_confidence"`
	AISpecialistEvidence   []string       `json:"ai_specialist_evidence"`
}

type ScreeningOutput struct {
	Profile  ResumeProfile `json:"profile"`
	Decision Decision      `json:"decision"`
}

func (o ScreeningOutput) Validate() error {
	switch o.Decision.Recommendation {
	case "dispatch", "review", "archive":
	default:
		return errors.New("invalid recommendation")
	}
	if err := o.Decision.ScoreBreakdown.Validate(); err != nil {
		return err
	}
	if o.Decision.AISpecialistConfidence < 0 || o.Decision.AISpecialistConfidence > 1 {
		return errors.New("ai_specialist_confidence must be between 0 and 1")
	}
	return nil
}

type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type TurnFinal struct {
	Action     string          `json:"action"`
	Evaluation ScreeningOutput `json:"evaluation"`
}

type AgentTurnOutput struct {
	Kind      string     `json:"kind"`
	ToolCalls []ToolCall `json:"tool_calls"`
	Final     *TurnFinal `json:"final"`
}

type ToolTrace struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	ItemCount  int    `json:"item_count"`
}

type SafeTrace struct {
	TraceID       string      `json:"trace_id"`
	KernelBuild   string      `json:"kernel_build"`
	StartedAt     time.Time   `json:"started_at"`
	FinishedAt    time.Time   `json:"finished_at"`
	Turns         int         `json:"turns"`
	ToolCallCount int         `json:"tool_call_count"`
	ToolCalls     []ToolTrace `json:"tool_calls"`
	InputTokens   int         `json:"input_tokens"`
	OutputTokens  int         `json:"output_tokens"`
	Status        string      `json:"status"`
}

type EvaluationError struct {
	Cause error
	Trace SafeTrace
}

func (e *EvaluationError) Error() string {
	return e.Cause.Error()
}

func (e *EvaluationError) Unwrap() error {
	return e.Cause
}

type AgentActionProposalV1 struct {
	ProposalVersion string          `json:"proposal_version"`
	TaskID          string          `json:"task_id"`
	PinID           string          `json:"pin_id"`
	Action          string          `json:"action"`
	Evaluation      ScreeningOutput `json:"evaluation"`
	Trace           SafeTrace       `json:"safe_trace"`
}
