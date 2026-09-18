// Package agent 只收集证据化画像与合规岗位匹配，不产生业务动作。
package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"unicode"

	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/tools"
)

type Line struct {
	Number int    `json:"number"`
	Page   int    `json:"page"`
	Text   string `json:"text"`
}
type Collector struct {
	Candidate  p.CandidateSnapshotV1
	TagCatalog []p.AbilityTagV1
	Profile    *p.CandidateProfileV1
	Matches    map[string]p.JobMatchV1
	Jobs       map[string]p.JobSnapshotV1
	Lines      []Line
	Done       bool
	Failed     bool
}

func NewCollector(text string, jobs []p.JobSnapshotV1) *Collector {
	c := &Collector{Matches: map[string]p.JobMatchV1{}, Jobs: map[string]p.JobSnapshotV1{}}
	for _, j := range jobs {
		c.Jobs[j.Ref] = j
	}
	for page, part := range strings.Split(text, "\f") {
		for _, line := range strings.Split(part, "\n") {
			c.Lines = append(c.Lines, Line{len(c.Lines) + 1, page + 1, line})
		}
	}
	return c
}
func Decode(value any, target any) error {
	raw, ok := value.([]byte)
	if !ok {
		var err error
		raw, err = json.Marshal(value)
		if err != nil {
			return err
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return errors.New("invalid tool arguments")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing tool arguments")
	}
	return nil
}
func norm(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
func (c *Collector) Verify(e p.EvidenceV1) bool {
	if len([]rune(norm(e.Quote))) < 8 || e.StartLine < 1 || e.EndLine < e.StartLine || e.EndLine > len(c.Lines) || e.EndLine-e.StartLine > 100 {
		return false
	}
	text := ""
	for _, line := range c.Lines[e.StartLine-1 : e.EndLine] {
		if line.Page != e.Page {
			return false
		}
		text += line.Text
	}
	return strings.Contains(norm(text), norm(e.Quote))
}
func (c *Collector) verifyAll(e []p.EvidenceV1) bool {
	if len(e) == 0 {
		return false
	}
	for _, v := range e {
		if !c.Verify(v) {
			return false
		}
	}
	return true
}
func (c *Collector) validateEvidence(e []p.EvidenceV1, field string) error {
	if len(e) == 0 {
		return tools.Invalid("evidence_required", field, "至少提供一条原文证据。")
	}
	for i, v := range e {
		at := fmt.Sprintf("%s[%d]", field, i)
		if len([]rune(norm(v.Quote))) < 8 {
			return tools.Invalid("evidence_too_short", at+".quote", "去除空白后至少 8 个字符，必须来自原文。")
		}
		if v.StartLine < 1 || v.EndLine < v.StartLine || v.EndLine > len(c.Lines) || v.EndLine-v.StartLine > 100 {
			return tools.Invalid("evidence_range", at, "起止行须位于目录的全局行范围内，跨度不得超过 101 行。")
		}
		for _, line := range c.Lines[v.StartLine-1 : v.EndLine] {
			if line.Page != v.Page {
				return tools.Invalid("evidence_page", at+".page", "页码与全局行号不一致；读取对应行后修正。")
			}
		}
		if !c.Verify(v) {
			return tools.Invalid("evidence_mismatch", at+".quote", "引用与指定页行的原文不匹配；读取该行区间并原样引用，不要改写证据。")
		}
	}
	return nil
}
func (c *Collector) SubmitProfile(profile p.CandidateProfileV1) error {
	if c.Done || c.Profile != nil {
		return tools.Invalid("profile_already_submitted", "claims", "画像已通过校验；继续尚未提交的岗位分析或 task_done。")
	}
	if len(profile.Claims) == 0 || len(profile.Claims) > 100 {
		return tools.Invalid("claims_required", "claims", "画像须包含 1 至 100 项有证据的描述。")
	}
	for i, claim := range profile.Claims {
		field := fmt.Sprintf("claims[%d]", i)
		switch claim.Kind {
		case "education", "project", "internship", "skill", "certificate", "major_direction", "agent_experience", "risk":
		default:
			return tools.Invalid("claim_kind", field+".kind", "使用 Schema 中允许的画像类型。")
		}
		if strings.TrimSpace(claim.Summary) == "" {
			return tools.Invalid("summary_required", field+".summary", "填写该项画像的简明描述。")
		}
		if err := c.validateEvidence(claim.Evidence, field+".evidence"); err != nil {
			return err
		}
	}
	seenTags := map[string]bool{}
	for i, tag := range profile.Tags {
		known := false
		for _, definition := range c.TagCatalog {
			if definition.Code == tag.Code {
				known = true
			}
		}
		field := fmt.Sprintf("tags[%d]", i)
		if !known || seenTags[tag.Code] {
			return tools.Invalid("tag_code", field+".code", "使用 tag_catalog 中的 code，同一标签只提交一次。")
		}
		if math.IsNaN(tag.Confidence) || tag.Confidence < 0 || tag.Confidence > 1 {
			return tools.Invalid("confidence_range", field+".confidence", "置信度须为 0 到 1。")
		}
		if tag.Status != "supported" && tag.Status != "needs_verification" {
			return tools.Invalid("tag_status", field+".status", "状态须为 supported 或 needs_verification。")
		}
		if err := c.validateEvidence(tag.Evidence, field+".evidence"); err != nil {
			return err
		}
		seenTags[tag.Code] = true
		if tag.Confidence < .8 {
			profile.Tags[i].Status = "needs_verification"
		}
	}
	c.Profile = &profile
	characters := 0
	for _, line := range c.Lines {
		characters += len([]rune(strings.TrimSpace(line.Text)))
	}
	hasExperience := false
	for _, claim := range profile.Claims {
		if claim.Kind == "project" || claim.Kind == "internship" || claim.Kind == "skill" {
			hasExperience = true
		}
	}
	if characters < 200 || !hasExperience {
		c.Profile.Risks = append(c.Profile.Risks, "profile_incomplete")
	}
	return nil
}
func (c *Collector) SubmitMatches(matches []p.JobMatchV1) error {
	if c.Done || c.Profile == nil || len(matches) == 0 {
		return tools.Invalid("profile_required", "matches", "须先成功提交画像，再提交当前岗位分析。")
	}
	seen := map[string]bool{}
	for i, m := range matches {
		field := fmt.Sprintf("matches[%d]", i)
		if _, ok := c.Jobs[m.JobRef]; !ok {
			return tools.Invalid("job_scope", field+".job_ref", "只允许当前输入中固定的岗位引用。")
		}
		if _, ok := c.Matches[m.JobRef]; ok || seen[m.JobRef] {
			return tools.Invalid("match_already_submitted", field+".job_ref", "岗位已提交，不要重复提交；完成后调用 task_done。")
		}
		seen[m.JobRef] = true
		if m.Dimensions.Validate() != nil {
			return tools.Invalid("dimensions_range", field+".dimensions", "所有评分维度须为 0 到 1 的有限数值。")
		}
		if math.IsNaN(m.Confidence) || m.Confidence < 0 || m.Confidence > 1 {
			return tools.Invalid("confidence_range", field+".confidence", "置信度须为 0 到 1。")
		}
		if strings.TrimSpace(m.Reason) == "" {
			return tools.Invalid("reason_required", field+".reason", "填写与当前岗位相关的分析理由。")
		}
		if err := c.validateEvidence(m.Evidence, field+".evidence"); err != nil {
			return err
		}
	}
	for _, m := range matches {
		for _, risk := range c.Profile.Risks {
			if risk == "profile_incomplete" {
				m.Dimensions.ResumeQuality = math.Min(m.Dimensions.ResumeQuality, .35)
				break
			}
		}
		d := m.Dimensions
		m.Score = math.Round((d.MajorMatch*.30+d.SkillsMatch*.20+d.ExperienceEvidence*.25+d.JobRequirement*.15+d.ResumeQuality*.10)*10000) / 10000
		m.Rank = 0
		c.Matches[m.JobRef] = m
	}
	return nil
}
func (c *Collector) Finish(status string) error {
	if c.Done {
		return errors.New("task already ended")
	}
	if status == "FAILED" {
		c.Done = true
		c.Failed = true
		return nil
	}
	if status != "DONE" || c.Profile == nil || len(c.Matches) != len(c.Jobs) {
		return tools.Invalid("completion_incomplete", "status", "画像和当前岗位分析都须提交并通过校验；否则修正缺失项或以 FAILED 结束。")
	}
	c.Done = true
	return nil
}
func (c *Collector) Ranked() []p.JobMatchV1 {
	result := make([]p.JobMatchV1, 0, len(c.Matches))
	for _, m := range c.Matches {
		result = append(result, m)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Score == result[j].Score {
			return result[i].JobRef < result[j].JobRef
		}
		return result[i].Score > result[j].Score
	})
	for i := range result {
		result[i].Rank = i + 1
	}
	return result
}
