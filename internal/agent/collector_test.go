package agent

import (
	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/tools"
	"testing"
)

func TestEvidenceScopeCoverageAndStableRanking(t *testing.T) {
	c := NewCollector("项目经历：负责 Go 服务开发和性能优化。", []p.JobSnapshotV1{{Ref: "b"}, {Ref: "a"}})
	evidence := p.EvidenceV1{Quote: "负责 Go 服务开发和性能优化", Page: 1, StartLine: 1, EndLine: 1}
	if err := c.Finish("DONE"); err == nil {
		t.Fatal("incomplete task accepted")
	}
	bad := evidence
	bad.Page = 2
	if c.Verify(bad) {
		t.Fatal("forged page accepted")
	}
	bad = evidence
	bad.Quote = "不存在的简历项目原文"
	if c.Verify(bad) {
		t.Fatal("forged quote accepted")
	}
	profile := p.CandidateProfileV1{Claims: []p.ProfileClaimV1{{Kind: "project", Summary: "服务开发", Evidence: []p.EvidenceV1{evidence}}}, Risks: []string{}}
	if err := c.SubmitProfile(profile); err != nil {
		t.Fatal(err)
	}
	if c.SubmitProfile(profile) == nil {
		t.Fatal("duplicate profile accepted")
	}
	match := p.JobMatchV1{JobRef: "outside", Confidence: .9, Reason: "服务项目", Evidence: []p.EvidenceV1{evidence}, Dimensions: p.ScoreBreakdown{MajorMatch: 1}}
	if c.SubmitMatches([]p.JobMatchV1{match}) == nil {
		t.Fatal("outside job accepted")
	}
	match.JobRef = "b"
	if err := c.SubmitMatches([]p.JobMatchV1{match}); err != nil {
		t.Fatal(err)
	}
	if c.SubmitMatches([]p.JobMatchV1{match}) == nil {
		t.Fatal("duplicate accepted")
	}
	if c.Finish("DONE") == nil {
		t.Fatal("partial coverage accepted")
	}
	match.JobRef = "a"
	if err := c.SubmitMatches([]p.JobMatchV1{match}); err != nil {
		t.Fatal(err)
	}
	if err := c.Finish("DONE"); err != nil {
		t.Fatal(err)
	}
	ranked := c.Ranked()
	if ranked[0].JobRef != "a" || ranked[0].Score != .3 || ranked[1].Rank != 2 {
		t.Fatal(ranked)
	}
	registry, err := tools.NewProviders(&Provider{Collector: c})
	if err != nil {
		t.Fatal(err)
	}
	if registry.Register(&Provider{Collector: c}) == nil {
		t.Fatal("frozen registry mutable")
	}
}
