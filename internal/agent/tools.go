package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"

	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/tools"
)

//go:embed tools.json
var catalog []byte

type Provider struct {
	Taxonomy    []p.MajorAliasV1
	Collector   *Collector
	Candidate   p.CandidateSnapshotV1
	Constraints p.DeterministicResultV1
}

func (v *Provider) Name() string { return "resume-match" }
func jobRequirement(j p.JobSnapshotV1) map[string]any {
	return map[string]any{"job_ref": j.Ref, "content_hash": j.ContentHash, "entity": j.Entity, "position_name": j.PositionName, "category": j.Category, "job_family": j.JobFamily, "location": j.Location, "education": j.Education, "required_majors": j.RequiredMajors, "responsibilities": j.Responsibilities, "department_name": j.DepartmentName}
}
func (v *Provider) Definitions() []tools.Definition {
	var defs []tools.Definition
	if err := json.Unmarshal(catalog, &defs); err != nil {
		panic(err)
	}
	return defs
}
func (v *Provider) Execute(ctx context.Context, call p.ToolCall) (tools.Result, error) {
	if err := ctx.Err(); err != nil {
		return tools.Result{}, err
	}
	c := v.Collector
	var value any
	switch call.Name {
	case "case.read_constraints":
		value = map[string]any{"volunteer_ref": v.Constraints.CurrentVolunteerRef, "job_refs": v.Constraints.JobRefs,
			"scope_policy": "只分析当前材料和已授权岗位；禁止扩大范围或产生业务动作"}
	case "candidate.read_context":
		value = map[string]any{"highest_major": v.Candidate.HighestMajor, "highest_education": v.Candidate.HighestEducation, "tag_catalog": c.TagCatalog}
	case "resume.list_sections":
		sections := []map[string]int{}
		for _, line := range c.Lines {
			if len(sections) == 0 || sections[len(sections)-1]["page"] != line.Page {
				sections = append(sections, map[string]int{"page": line.Page, "start_line": line.Number, "end_line": line.Number})
			} else {
				sections[len(sections)-1]["end_line"] = line.Number
			}
		}
		value = sections
	case "resume.read_sections":
		var a struct {
			Start int `json:"start_line"`
			End   int `json:"end_line"`
		}
		if err := Decode(call.Arguments, &a); err != nil {
			return tools.Result{}, err
		}
		if a.Start < 1 || a.End < a.Start || a.End > len(c.Lines) || a.End-a.Start > 199 {
			return tools.Result{}, errors.New("invalid line range, maximum 200 lines")
		}
		value = c.Lines[a.Start-1 : a.End]
	case "resume.search_evidence", "job.search_requirements", "taxonomy.lookup_major":
		var a struct {
			Query string `json:"query"`
		}
		if err := Decode(call.Arguments, &a); err != nil {
			return tools.Result{}, err
		}
		if strings.TrimSpace(a.Query) == "" {
			return tools.Result{}, errors.New("query required")
		}
		if call.Name == "resume.search_evidence" {
			lines := []Line{}
			for _, line := range c.Lines {
				if strings.Contains(strings.ToLower(line.Text), strings.ToLower(a.Query)) {
					lines = append(lines, line)
					if len(lines) == 50 {
						break
					}
				}
			}
			value = lines
		} else if call.Name == "taxonomy.lookup_major" {
			matches := []string{}
			for _, alias := range v.Taxonomy {
				q, n := norm(strings.ToLower(a.Query)), norm(strings.ToLower(alias.Name))
				if q == n || (alias.MatchType == "contains" && strings.Contains(q, n)) {
					matches = append(matches, alias.Category)
				}
			}
			value = map[string]any{"query": a.Query, "matches": matches}
		} else {
			jobs := []map[string]any{}
			for _, ref := range v.Constraints.JobRefs {
				j := c.Jobs[ref]
				if strings.Contains(strings.ToLower(j.Responsibilities+" "+strings.Join(j.RequiredMajors, " ")), strings.ToLower(a.Query)) {
					jobs = append(jobs, jobRequirement(j))
				}
			}
			value = jobs
		}
	case "jobs.list_candidates":
		jobs := []map[string]string{}
		for _, ref := range v.Constraints.JobRefs {
			j := c.Jobs[ref]
			jobs = append(jobs, map[string]string{"job_ref": ref, "position_name": j.PositionName, "department_name": j.DepartmentName})
		}
		value = jobs
	case "job.read_requirement":
		var a struct {
			Ref string `json:"job_ref"`
		}
		if err := Decode(call.Arguments, &a); err != nil {
			return tools.Result{}, err
		}
		j, ok := c.Jobs[a.Ref]
		if !ok {
			return tools.Result{}, errors.New("job outside compliant pool")
		}
		value = jobRequirement(j)
	case "evidence.verify_quotes":
		var a struct {
			Evidence []p.EvidenceV1 `json:"evidence"`
		}
		if err := Decode(call.Arguments, &a); err != nil {
			return tools.Result{}, err
		}
		value = map[string]bool{"verified": c.verifyAll(a.Evidence)}
	case "candidate_profile.submit":
		var profile p.CandidateProfileV1
		if err := Decode(call.Arguments, &profile); err != nil {
			return tools.Result{}, err
		}
		if err := c.SubmitProfile(profile); err != nil {
			return tools.Result{}, err
		}
		value = map[string]bool{"accepted": true}
	case "job_match.submit":
		var a struct {
			Matches []p.JobMatchV1 `json:"matches"`
		}
		if err := Decode(call.Arguments, &a); err != nil {
			return tools.Result{}, err
		}
		if err := c.SubmitMatches(a.Matches); err != nil {
			return tools.Result{}, err
		}
		value = map[string]int{"covered_jobs": len(c.Matches), "required_jobs": len(c.Jobs)}
	case "task_done":
		var a struct {
			Status string `json:"status"`
		}
		if err := Decode(call.Arguments, &a); err != nil {
			return tools.Result{}, err
		}
		if err := c.Finish(a.Status); err != nil {
			return tools.Result{}, err
		}
		value = map[string]bool{"accepted": true}
	default:
		return tools.Result{}, errors.New("unknown tool")
	}
	raw, err := json.Marshal(value)
	return tools.Result{Payload: raw, ItemCount: 1}, err
}
