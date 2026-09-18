package tools

import (
	"errors"
	"strings"
	"testing"
)

func TestSchemaFeedbackLocatesFailureWithoutSubmittedData(t *testing.T) {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"claims": map[string]any{"type": "array", "items": map[string]any{"type": "object", "required": []string{"confidence"}, "properties": map[string]any{"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1}}}},
	}}
	for _, value := range []any{"private-resume-secret", 2, nil} {
		claim := map[string]any{}
		if value != nil {
			claim["confidence"] = value
		}
		feedback := Feedback(schemaFeedback(schema, map[string]any{"claims": []any{claim}}))
		if feedback.Code != "schema_invalid" || feedback.Field != "arguments.claims[0].confidence" || strings.Contains(feedback.Error(), "private-resume-secret") {
			t.Fatal("unsafe or imprecise schema feedback")
		}
	}
	feedback := Feedback(schemaFeedback(schema, map[string]any{"private-resume-secret": "private-resume-secret"}))
	if strings.Contains(feedback.Error(), "private-resume-secret") || feedback.Field != "arguments" {
		t.Fatal("unknown argument name leaked")
	}
	if strings.Contains(Feedback(errors.New("private-provider-secret")).Error(), "private-provider-secret") {
		t.Fatal("provider error leaked")
	}
}
