package protocol

import (
	"encoding/hex"
	"errors"
	"strings"
)

const (
	TaskProtocolVersion    = "resume-analysis/v5"
	ResumeJobMatchTaskKind = "candidate.application_assessment"
	TaskResultVersion      = "resume-application-assessment/v1"
)

type TaskPinV1 struct {
	PinID               string `json:"pin_id"`
	KernelBuild         string `json:"kernel_build"`
	ProtocolVersion     string `json:"protocol_version"`
	ToolsetVersion      string `json:"toolset_version"`
	ResultSchemaVersion string `json:"result_schema_version"`
	PolicyVersion       string `json:"policy_version"`
	InstructionVersion  string `json:"instruction_version"`
	ModelConfigRevision string `json:"model_config_revision"`
}

type TaskBudgetV1 struct {
	MaxTurns           int `json:"max_turns"`
	MaxToolCalls       int `json:"max_tool_calls"`
	MaxDurationSeconds int `json:"max_duration_seconds"`
	MaxTokens          int `json:"max_tokens"`
	MaxContextTokens   int `json:"max_context_tokens,omitempty"`
}

type CandidateSnapshotV1 = AnalysisCandidateV1

const MaxTextBytes = 1 << 20
const MaxRequestBytes = 2 << 20

type ResumeTextV2 struct {
	FileSHA256       string   `json:"file_sha256"`
	TextSHA256       string   `json:"text_sha256"`
	ExtractorVersion string   `json:"extractor_version"`
	Pages            []string `json:"pages"`
	Status           string   `json:"status"`
	Warnings         []string `json:"warnings"`
}

type VolunteerSnapshotV1 struct {
	Ref          string       `json:"ref"`
	PositionName string       `json:"position_name"`
	Entity       string       `json:"entity"`
	ApplyDate    string       `json:"apply_date"`
	Rejected     bool         `json:"rejected"`
	ResumeText   ResumeTextV2 `json:"resume_text"`
}

type JobSnapshotV1 = AnalysisJobV1

type WorkflowSnapshotV1 struct {
	Revision int64 `json:"revision"`
}

type CaseSnapshotV1 struct {
	TagCatalog []AbilityTagV1        `json:"tag_catalog,omitempty"`
	Candidate  CandidateSnapshotV1   `json:"candidate"`
	Workflow   WorkflowSnapshotV1    `json:"workflow"`
	Volunteers []VolunteerSnapshotV1 `json:"volunteers"`
	Jobs       []JobSnapshotV1       `json:"jobs"`
}

type TaskEnvelopeV1 struct {
	ProtocolVersion string         `json:"protocol_version"`
	TaskKind        string         `json:"task_kind"`
	TaskID          string         `json:"task_id"`
	IdempotencyKey  string         `json:"idempotency_key"`
	Trigger         string         `json:"trigger"`
	Pin             TaskPinV1      `json:"pin"`
	Snapshot        CaseSnapshotV1 `json:"snapshot"`
	Model           ModelConfig    `json:"model"`
	Budget          TaskBudgetV1   `json:"budget"`
}

func validateSHA256(value string) error {
	if len(value) != 64 {
		return errors.New("checksum must be a 64-character SHA-256")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return errors.New("checksum must be hexadecimal")
	}
	return nil
}

func (e TaskEnvelopeV1) Validate() error {
	if e.ProtocolVersion != TaskProtocolVersion || e.TaskKind != ResumeJobMatchTaskKind {
		return errors.New("unsupported protocol_version or task_kind")
	}
	if strings.TrimSpace(e.TaskID) == "" || strings.TrimSpace(e.IdempotencyKey) == "" {
		return errors.New("task_id and idempotency_key are required")
	}
	if e.Pin.ProtocolVersion != TaskProtocolVersion || e.Pin.ResultSchemaVersion != TaskResultVersion {
		return errors.New("unsupported task pin")
	}
	for _, value := range []string{e.Pin.PinID, e.Pin.KernelBuild, e.Pin.ToolsetVersion, e.Pin.InstructionVersion, e.Pin.PolicyVersion, e.Pin.ModelConfigRevision} {
		if strings.TrimSpace(value) == "" || len(value) > 128 {
			return errors.New("invalid task pin")
		}
	}
	b := e.Budget
	if b.MaxTurns < 1 || b.MaxTurns > 64 || b.MaxToolCalls < 1 || b.MaxToolCalls > 512 || b.MaxDurationSeconds < 1 || b.MaxDurationSeconds > 1800 || b.MaxTokens < 1 || b.MaxTokens > 1000000 {
		return errors.New("invalid task budget")
	}
	if b.MaxContextTokens != 0 && (b.MaxContextTokens < 1024 || b.MaxContextTokens > 1000000) {
		return errors.New("invalid context budget")
	}
	if e.Snapshot.Candidate.Ref == "" || len(e.Snapshot.Volunteers) == 0 || len(e.Snapshot.Volunteers) > 100 || len(e.Snapshot.Jobs) > 2000 {
		return errors.New("invalid snapshot scope")
	}
	seen := map[string]bool{}
	for _, v := range e.Snapshot.Volunteers {
		if v.Ref == "" || seen[v.Ref] {
			return errors.New("invalid volunteer reference")
		}
		seen[v.Ref] = true
	}
	seen = map[string]bool{}
	for _, j := range e.Snapshot.Jobs {
		if j.Ref == "" || seen[j.Ref] {
			return errors.New("invalid job reference")
		}
		seen[j.Ref] = true
		if err := validateSHA256(j.ContentHash); err != nil {
			return err
		}
	}
	return nil
}

type EvidenceV1 struct {
	Quote     string `json:"quote"`
	Page      int    `json:"page"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type ProfileClaimV1 struct {
	Details    ClaimDetailsV1 `json:"details"`
	Confidence float64        `json:"confidence"`
	Kind       string         `json:"kind"`
	Summary    string         `json:"summary"`
	Evidence   []EvidenceV1   `json:"evidence"`
}

type ClaimDetailsV1 struct {
	SchoolName string `json:"school_name,omitempty"`
	Degree     string `json:"degree,omitempty"`
	Major      string `json:"major,omitempty"`
	Period     string `json:"period,omitempty"`
	Name       string `json:"name,omitempty"`
	Role       string `json:"role,omitempty"`
}

type CandidateProfileV1 struct {
	SourceText string           `json:"source_text,omitempty"`
	Claims     []ProfileClaimV1 `json:"claims"`
	Risks      []string         `json:"risks"`
	Tags       []TagAssertionV1 `json:"tags,omitempty"`
}

type AbilityTagV1 struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

type TagAssertionV1 struct {
	Code       string       `json:"code"`
	Confidence float64      `json:"confidence"`
	Status     string       `json:"status"`
	Evidence   []EvidenceV1 `json:"evidence"`
}

type JobMatchV1 struct {
	JobRef     string         `json:"job_ref"`
	Dimensions ScoreBreakdown `json:"dimensions"`
	Confidence float64        `json:"confidence"`
	Evidence   []EvidenceV1   `json:"evidence"`
	Risks      []string       `json:"risks"`
	Reason     string         `json:"reason"`
	Score      float64        `json:"score"`
	Rank       int            `json:"rank"`
}

type DeterministicResultV1 struct {
	FirstDegreeTagRef   string   `json:"first_degree_tag_ref"`
	HighestDegreeTagRef string   `json:"highest_degree_tag_ref"`
	VolunteerOrder      []string `json:"volunteer_order"`
	CurrentVolunteerRef string   `json:"current_volunteer_ref"`
	AdmissionPassed     bool     `json:"admission_passed"`
	AdmissionRuleRef    string   `json:"admission_rule_ref"`
	JobRefs             []string `json:"job_refs"`
	Status              string   `json:"status"`
}

type TaskManifestV1 struct {
	ReusedFromTaskID string            `json:"reused_from_task_id,omitempty"`
	InputHash        string            `json:"input_hash"`
	ResumeChecksum   string            `json:"resume_checksum"`
	CoveredJobs      []string          `json:"covered_jobs"`
	ToolVersions     map[string]string `json:"tool_versions"`
	Warnings         []string          `json:"warnings"`
	TerminalState    string            `json:"terminal_state"`
	FailureCode      string            `json:"failure_code,omitempty"`
	OCRPages         int               `json:"ocr_pages"`
}

type TaskResultV1 struct {
	ProtocolVersion  string                `json:"protocol_version"`
	TaskID           string                `json:"task_id"`
	IdempotencyKey   string                `json:"idempotency_key"`
	Pin              TaskPinV1             `json:"pin"`
	WorkflowRevision int64                 `json:"workflow_revision"`
	Deterministic    DeterministicResultV1 `json:"deterministic"`
	Profile          *CandidateProfileV1   `json:"profile"`
	Matches          []JobMatchV1          `json:"matches"`
	Manifest         TaskManifestV1        `json:"manifest"`
	Trace            SafeTrace             `json:"safe_trace"`
}
