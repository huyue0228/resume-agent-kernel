package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	p "resume-agent-kernel/internal/protocol"
	"strings"
	"testing"
)

type fakeEvaluator struct{ called bool }

func (e *fakeEvaluator) ExecuteAnalysis(context.Context, p.AnalysisRequestV1, string) (p.AnalysisResponseV1, error) {
	e.called = true
	return p.AnalysisResponseV1{}, nil
}
func (e *fakeEvaluator) Capabilities() (p.KernelCapabilitiesV1, error) {
	return p.KernelCapabilitiesV1{ProtocolVersion: p.TaskProtocolVersion, ResultSchemaVersion: p.TaskResultVersion, TaskKinds: []string{p.ResumeJobMatchTaskKind}, KernelBuild: "build-1", ToolsetVersion: "tools/future", InstructionVersion: "instructions/future"}, nil
}
func TestCapabilitiesRequireAuthenticationAndPublishOpaqueVersions(t *testing.T) {
	h := New(&fakeEvaluator{}, "secret", nil)
	for _, token := range []string{"", "secret"} {
		req := httptest.NewRequest("GET", "/v2/capabilities", nil)
		req.Header.Set("X-Agent-Kernel-Token", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if token == "" {
			if w.Code != 401 {
				t.Fatal(w.Code)
			}
			continue
		}
		var c p.KernelCapabilitiesV1
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &c) != nil || c.InstructionVersion != "instructions/future" {
			t.Fatal(w.Body.String())
		}
	}
}
func TestOldEndpointIsRemovedAndNewEndpointAuthenticatesBeforeParsing(t *testing.T) {
	e := &fakeEvaluator{}
	h := New(e, "secret", nil)
	for path, status := range map[string]int{"/v1/evaluate": 404, "/v2/tasks/execute": 401} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader("not-json")))
		if w.Code != status || e.called {
			t.Fatalf("path %s status %d called %v", path, w.Code, e.called)
		}
	}
}
