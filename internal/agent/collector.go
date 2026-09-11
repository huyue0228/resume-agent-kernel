// Package agent 只收集证据化画像与合规岗位匹配，不产生业务动作。
package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strings"
	"unicode"

	p "resume-agent-kernel/internal/protocol"
)

type Line struct {
	Number int    `json:"number"`
	Page   int    `json:"page"`
	Text   string `json:"text"`
}
type Collector struct {
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
func (c *Collector) SubmitProfile(profile p.CandidateProfileV1) error {
	if c.Done || c.Profile != nil {
		return errors.New("profile already submitted")
	}
	if len(profile.Claims) == 0 || len(profile.Claims) > 100 {
		return errors.New("profile claims required")
	}
	for _, claim := range profile.Claims {
		switch claim.Kind {
		case "education", "project", "internship", "skill", "certificate", "major_direction", "agent_experience", "risk":
		default:
			return errors.New("invalid profile claim kind")
		}
		if strings.TrimSpace(claim.Summary) == "" || !c.verifyAll(claim.Evidence) {
			return errors.New("profile evidence invalid")
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
		if !known || seenTags[tag.Code] || math.IsNaN(tag.Confidence) || tag.Confidence < 0 || tag.Confidence > 1 || (tag.Status != "supported" && tag.Status != "needs_verification") || !c.verifyAll(tag.Evidence) {
			return errors.New("tag code, confidence or evidence invalid")
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
		return errors.New("invalid match submission")
	}
	seen := map[string]bool{}
	for _, m := range matches {
		if _, ok := c.Jobs[m.JobRef]; !ok {
			return errors.New("job reference outside compliant pool")
		}
		if _, ok := c.Matches[m.JobRef]; ok || seen[m.JobRef] {
			return errors.New("duplicate job submission")
		}
		seen[m.JobRef] = true
		if m.Dimensions.Validate() != nil || math.IsNaN(m.Confidence) || m.Confidence < 0 || m.Confidence > 1 || strings.TrimSpace(m.Reason) == "" || !c.verifyAll(m.Evidence) {
			return errors.New("match dimensions or evidence invalid")
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
		return errors.New("profile and complete job coverage required")
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
