package runtime

import (
	"context"
	"errors"
	p "resume-agent-kernel/internal/protocol"
	"testing"
)

func TestFrozenInternalVersionMismatchFailsBeforeDocumentWork(t *testing.T) {
	for _, field := range []string{"build", "tools", "instructions"} {
		s := NewService("test")
		docs := &testDocument{}
		s.Documents = docs
		e := taskEnvelope()
		switch field {
		case "build":
			e.Pin.KernelBuild = "old"
		case "tools":
			e.Pin.ToolsetVersion = "old"
		case "instructions":
			e.Pin.InstructionVersion = "old"
		}
		_, err := s.Execute(context.Background(), e, "")
		if !errors.Is(err, p.ErrVersionUnavailable) || docs.calls != 0 {
			t.Fatalf("%s mismatch not rejected before document work: %v", field, err)
		}
	}
}
func TestPolicyVersionBelongsToPlatform(t *testing.T) {
	e := taskEnvelope()
	e.Pin.PolicyVersion = "platform-policy/new"
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}
