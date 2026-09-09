package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"resume-agent-kernel/internal/agent"
	p "resume-agent-kernel/internal/protocol"
	"strings"
	"testing"
)

func input(pages ...string) p.ResumeTextV2 {
	hash := sha256.Sum256([]byte(strings.Join(pages, "\f")))
	return p.ResumeTextV2{Pages: pages, FileSHA256: strings.Repeat("a", 64), TextSHA256: hex.EncodeToString(hash[:]), ExtractorVersion: "test/v2", Status: "ready"}
}
func TestCanonicalPagesAndEvidence(t *testing.T) {
	v := input("中文第一行\nEnglish second line\n", "", "负责后端服务开发与测试工作\n")
	doc, err := ReadText(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	c := agent.NewCollector(doc.Text, nil)
	if len(c.Lines) != 6 || c.Lines[3].Page != 2 || c.Lines[3].Text != "" || c.Lines[4].Page != 3 {
		t.Fatal("page/line drift")
	}
	e := p.EvidenceV1{Page: 3, StartLine: 5, EndLine: 5, Quote: "负责后端服务开发与测试工作"}
	if !c.Verify(e) {
		t.Fatal("valid quote rejected")
	}
	e.Page = 2
	if c.Verify(e) {
		t.Fatal("wrong page accepted")
	}
}
func TestRejectInvalidText(t *testing.T) {
	badHash := input("valid text")
	badHash.TextSHA256 = strings.Repeat("b", 64)
	incomplete := input("readable but incomplete")
	incomplete.Status = "needs_attention"
	for _, v := range []p.ResumeTextV2{input(), input(""), input("a\rb"), input("a\fb"), input("a\x00b"), input(strings.Repeat("中", p.MaxTextBytes/3+1)), badHash, incomplete} {
		if _, err := ReadText(context.Background(), v); err == nil {
			t.Fatal("invalid text accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadText(ctx, input("valid text")); err != context.Canceled {
		t.Fatal(err)
	}
}
