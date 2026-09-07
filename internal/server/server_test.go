package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"resume-agent-kernel/internal/protocol"
)

type fakeEvaluator struct {
	called bool
}

func (e *fakeEvaluator) Evaluate(context.Context, protocol.CaseEnvelopeV2, string) (protocol.AgentActionProposalV1, error) {
	e.called = true
	return protocol.AgentActionProposalV1{}, nil
}

func TestHealthPublishesRuntimeContract(t *testing.T) {
	handler := New(&fakeEvaluator{}, "secret", "build-1", nil)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["build"] != "build-1" || payload["protocol_version"] != protocol.ProtocolVersion {
		t.Fatalf("unexpected health payload: %#v", payload)
	}
}

func TestEvaluateRejectsMissingTokenBeforeParsingBody(t *testing.T) {
	evaluator := &fakeEvaluator{}
	handler := New(evaluator, "secret", "build-1", nil)
	request := httptest.NewRequest(http.MethodPost, "/v1/evaluate", strings.NewReader("not-json"))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized || evaluator.called {
		t.Fatalf("unexpected unauthorized result: status=%d called=%v", response.Code, evaluator.called)
	}
}
