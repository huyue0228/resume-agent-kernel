package protocol

import (
	"encoding/json"
	"errors"
)

type KernelCapabilitiesV1 struct {
	ProtocolVersion     string   `json:"protocol_version"`
	ResultSchemaVersion string   `json:"result_schema_version"`
	TaskKinds           []string `json:"task_kinds"`
	KernelBuild         string   `json:"kernel_build"`
	ToolsetVersion      string   `json:"toolset_version"`
	InstructionVersion  string   `json:"instruction_version"`
	Mock                bool     `json:"mock"`
}

var ErrVersionUnavailable = errors.New("kernel_version_unavailable")

// AnalysisRequestV5 是唯一公开的候选人级输入；不接受准入规则、历史志愿或 HC。
type AnalysisRequestV5 struct {
	ProtocolVersion  string          `json:"protocol_version"`
	TaskKind         string          `json:"task_kind"`
	TaskID           string          `json:"task_id"`
	IdempotencyKey   string          `json:"idempotency_key"`
	Trigger          string          `json:"trigger"`
	WorkflowRevision int64           `json:"workflow_revision"`
	Pin              TaskPinV1       `json:"pin"`
	Scope            AnalysisScopeV5 `json:"scope"`
	Model            ModelConfig     `json:"model"`
	Budget           TaskBudgetV1    `json:"budget"`
}

type AnalysisScopeV5 struct {
	Candidate    AnalysisCandidateV1 `json:"candidate"`
	VolunteerRef string              `json:"volunteer_ref"`
	ResumeText   ResumeTextV2        `json:"resume_text"`
	Jobs         []AnalysisJobV1     `json:"jobs"`
	TagCatalog   []AbilityTagV1      `json:"tag_catalog,omitempty"`
}
type AnalysisCandidateV1 struct {
	Ref              string `json:"ref"`
	HighestMajor     string `json:"highest_major"`
	HighestEducation string `json:"highest_education"`
}
type AnalysisJobV1 struct {
	Ref              string   `json:"ref"`
	ContentHash      string   `json:"content_hash"`
	Entity           string   `json:"entity"`
	PublicName       string   `json:"public_name"`
	PositionName     string   `json:"position_name"`
	Category         string   `json:"category"`
	JobFamily        string   `json:"job_family"`
	Location         string   `json:"location"`
	Education        string   `json:"education"`
	RequiredMajors   []string `json:"required_majors"`
	Responsibilities string   `json:"responsibilities"`
	DepartmentRef    string   `json:"department_ref"`
	DepartmentName   string   `json:"department_name"`
}

func (a AnalysisRequestV5) TaskInput() (TaskEnvelopeV1, error) {
	if a.WorkflowRevision < 0 || a.Scope.VolunteerRef == "" || len(a.Scope.Jobs) != 1 {
		return TaskEnvelopeV1{}, errors.New("invalid admitted scope")
	}
	jobs := []JobSnapshotV1{}
	data, _ := json.Marshal(a.Scope.Jobs)
	if err := json.Unmarshal(data, &jobs); err != nil {
		return TaskEnvelopeV1{}, err
	}
	e := TaskEnvelopeV1{ProtocolVersion: a.ProtocolVersion, TaskKind: a.TaskKind, TaskID: a.TaskID,
		IdempotencyKey: a.IdempotencyKey, Trigger: a.Trigger, Pin: a.Pin, Model: a.Model, Budget: a.Budget,
		Snapshot: CaseSnapshotV1{Candidate: CandidateSnapshotV1{Ref: a.Scope.Candidate.Ref, HighestMajor: a.Scope.Candidate.HighestMajor, HighestEducation: a.Scope.Candidate.HighestEducation},
			Workflow: WorkflowSnapshotV1{Revision: a.WorkflowRevision}, Jobs: jobs, TagCatalog: a.Scope.TagCatalog,
			Volunteers: []VolunteerSnapshotV1{{Ref: a.Scope.VolunteerRef, ResumeText: a.Scope.ResumeText}}}}
	return e, e.Validate()
}

type AnalysisResponseV5 struct {
	ProtocolVersion  string              `json:"protocol_version"`
	TaskID           string              `json:"task_id"`
	IdempotencyKey   string              `json:"idempotency_key"`
	Pin              TaskPinV1           `json:"pin"`
	WorkflowRevision int64               `json:"workflow_revision"`
	Profile          *CandidateProfileV1 `json:"profile"`
	Matches          []JobMatchV1        `json:"matches"`
	Manifest         TaskManifestV1      `json:"manifest"`
	Trace            SafeTrace           `json:"safe_trace"`
}
