package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"resume-agent-kernel/internal/agent"
	"resume-agent-kernel/internal/contract"
	"resume-agent-kernel/internal/llmloop"
	"resume-agent-kernel/internal/model"
	"resume-agent-kernel/internal/pipeline"
	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/tools"
)

func (s *Service) ExecuteAnalysis(ctx context.Context, a p.AnalysisRequestV5, key string) (p.AnalysisResponseV5, error) {
	e, err := a.TaskInput()
	if err != nil {
		return p.AnalysisResponseV5{}, err
	}
	r, err := s.Execute(ctx, e, key)
	if err != nil {
		return p.AnalysisResponseV5{}, err
	}
	result := p.AnalysisResponseV5{ProtocolVersion: r.ProtocolVersion, TaskID: r.TaskID, IdempotencyKey: r.IdempotencyKey,
		Pin: r.Pin, WorkflowRevision: r.WorkflowRevision, Profile: r.Profile, Matches: r.Matches, Manifest: r.Manifest, Trace: r.Trace}
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if err = contract.Validate("response", raw); err != nil {
		return p.AnalysisResponseV5{}, errors.New("analysis response contract invalid")
	}
	return result, nil
}

func (s *Service) Execute(ctx context.Context, e p.TaskEnvelopeV1, key string) (p.TaskResultV1, error) {
	if err := e.Validate(); err != nil {
		return p.TaskResultV1{}, err
	}
	capabilities, err := s.Capabilities()
	if err != nil {
		return p.TaskResultV1{}, err
	}
	if e.Pin.KernelBuild != capabilities.KernelBuild || e.Pin.ToolsetVersion != capabilities.ToolsetVersion || e.Pin.InstructionVersion != capabilities.InstructionVersion {
		return p.TaskResultV1{}, p.ErrVersionUnavailable
	}
	raw, _ := json.Marshal(e)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	return s.tasks.Execute(ctx, e.IdempotencyKey, hash, func() (p.TaskResultV1, error) { return s.runTask(ctx, e, key, hash) })
}

func (s *Service) runTask(ctx context.Context, e p.TaskEnvelopeV1, key, hash string) (result p.TaskResultV1, err error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(e.Budget.MaxDurationSeconds)*time.Second)
	defer cancel()
	result = p.TaskResultV1{ProtocolVersion: p.TaskProtocolVersion, TaskID: e.TaskID, IdempotencyKey: e.IdempotencyKey, Pin: e.Pin, WorkflowRevision: e.Snapshot.Workflow.Revision, Matches: []p.JobMatchV1{}, Manifest: p.TaskManifestV1{InputHash: hash, CoveredJobs: []string{}, ToolVersions: map[string]string{}, Warnings: []string{}, TerminalState: "FAILED"}, Trace: p.SafeTrace{TraceID: randomID(), KernelBuild: s.Build, StartedAt: time.Now().UTC(), ToolCalls: []p.ToolTrace{}}}
	defer func() {
		result.Trace.FinishedAt = time.Now().UTC()
		result.Trace.Status = result.Manifest.TerminalState
		if err != nil {
			var stopped *llmloop.StopError
			if result.Manifest.FailureCode == "" {
				result.Manifest.FailureCode = "analysis_failed"
			}
			if errors.Is(err, context.DeadlineExceeded) {
				result.Manifest.FailureCode = "task_timeout"
				if result.Trace.Budget != nil {
					result.Trace.Budget.StopReason = "timeout"
				}
			} else if errors.Is(err, context.Canceled) {
				result.Manifest.FailureCode = "task_cancelled"
				if result.Trace.Budget != nil {
					result.Trace.Budget.StopReason = "cancelled"
				}
			} else if errors.As(err, &stopped) {
				result.Manifest.FailureCode = map[string]string{"token_limit": "token_budget_exhausted", "next_request": "next_request_budget_insufficient", "context_limit": "context_limit_exceeded", "turn_limit": "turn_budget_exhausted", "tool_limit": "tool_budget_exhausted", "no_progress": "analysis_stalled"}[stopped.Reason]
			} else if strings.Contains(err.Error(), "budget") {
				result.Manifest.FailureCode = "budget_exhausted"
			} else if errors.Is(err, model.ErrRateLimited) {
				result.Manifest.FailureCode = "model_rate_limited"
			} else if errors.Is(err, model.ErrConnection) {
				result.Manifest.FailureCode = "model_connection_error"
			} else if errors.Is(err, model.ErrInvalidResponse) || errors.Is(err, llmloop.ErrInvalidOutput) {
				result.Manifest.FailureCode = "model_output_invalid"
			} else if errors.Is(err, llmloop.ErrIncomplete) {
				result.Manifest.FailureCode = "materials_incomplete"
			}
			result.Manifest.Warnings = append(result.Manifest.Warnings, "分析未完成，请人工处理或重试")
			err = nil
		}
	}()
	var volunteer *p.VolunteerSnapshotV1
	var jobs []p.JobSnapshotV1
	started := time.Now()
	// 招聘规则已由平台完成，这里只校验单候选人分析范围，不重新排序或准入。
	if len(e.Snapshot.Volunteers) != 1 || len(e.Snapshot.Jobs) == 0 {
		return result, errors.New("invalid admitted analysis scope")
	}
	volunteer = &e.Snapshot.Volunteers[0]
	jobs = e.Snapshot.Jobs
	result.Deterministic = p.DeterministicResultV1{Status: "ready", AdmissionPassed: true, CurrentVolunteerRef: volunteer.Ref, VolunteerOrder: []string{volunteer.Ref}, JobRefs: []string{}}
	for _, j := range jobs {
		result.Deterministic.JobRefs = append(result.Deterministic.JobRefs, j.Ref)
	}
	result.Trace.ToolCalls = append(result.Trace.ToolCalls, p.ToolTrace{Name: "pipeline.prepare", Status: result.Deterministic.Status, DurationMS: time.Since(started).Milliseconds()})
	started = time.Now()
	document, err := pipeline.ReadText(ctx, volunteer.ResumeText)
	status := "ok"
	if err != nil {
		status = "error"
	}
	result.Trace.ToolCalls = append(result.Trace.ToolCalls, p.ToolTrace{Name: "pipeline.text_validate", Status: status, DurationMS: time.Since(started).Milliseconds()})
	if err != nil {
		result.Manifest.FailureCode = "text_invalid"
		return result, err
	}
	result.Manifest.ResumeChecksum = document.Checksum
	collector := agent.NewCollector(document.Text, jobs)
	collector.TagCatalog = e.Snapshot.TagCatalog
	client, err := model.NewHTTPClient(e.Model, key)
	if err != nil {
		return result, err
	}
	// 岗位较多时分片，共享第一片提交的画像；总预算仍覆盖整个候选人。
	for offset := 0; offset < len(jobs); offset += 24 {
		chunk := jobs[offset:min(offset+24, len(jobs))]
		part := agent.NewCollector(document.Text, chunk)
		part.Candidate = e.Snapshot.Candidate
		part.TagCatalog = e.Snapshot.TagCatalog
		part.Profile = collector.Profile
		constraints := result.Deterministic
		constraints.JobRefs = []string{}
		for _, j := range chunk {
			constraints.JobRefs = append(constraints.JobRefs, j.Ref)
		}
		providers := append([]tools.Provider{&agent.Provider{Collector: part, Candidate: e.Snapshot.Candidate, Constraints: constraints}}, s.externalProviders...)
		registry, registryErr := tools.NewProviders(providers...)
		if registryErr != nil {
			return result, registryErr
		}
		for _, tool := range registry.Catalog() {
			result.Manifest.ToolVersions[tool.Name] = e.Pin.ToolsetVersion
			if tool.Version != "" {
				result.Manifest.ToolVersions[tool.Name] = tool.Version
			}
		}
		if err = llmloop.RunCandidate(ctx, client, registry, part, e.Budget, &result.Trace); err != nil {
			return result, err
		}
		collector.Profile = part.Profile
		for ref, match := range part.Matches {
			collector.Matches[ref] = match
		}
	}
	result.Profile = collector.Profile
	result.Profile.SourceText = document.Text // 协议校验后的平台全文，不由模型提交；不进入 SafeTrace。
	result.Matches = collector.Ranked()
	for _, m := range result.Matches {
		result.Manifest.CoveredJobs = append(result.Manifest.CoveredJobs, m.JobRef)
	}
	result.Manifest.TerminalState = "DONE"
	return result, nil
}
