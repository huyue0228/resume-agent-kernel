package contract

import (
	"encoding/json"
	p "resume-agent-kernel/internal/protocol"
	"testing"
)

func TestPublishedFixturesAndGoWireRoundTrip(t *testing.T) {
	for _, name := range []string{"request", "response"} {
		raw, err := Bundle.ReadFile("bundle/" + name + ".example.json")
		if err != nil {
			t.Fatal(err)
		}
		if err = Validate(name, raw); err != nil {
			t.Fatal(err)
		}
		var value any
		if name == "request" {
			value = &p.AnalysisRequestV1{}
		} else {
			value = &p.AnalysisResponseV1{}
		}
		if err = json.Unmarshal(raw, value); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(value)
		if err = Validate(name, encoded); err != nil {
			t.Fatalf("Go serialization differs from contract: %v", err)
		}
	}
}
func TestRequestRejectsSchoolRulesCapacityAndOutOfScopeFields(t *testing.T) {
	raw, _ := Bundle.ReadFile("bundle/request.example.json")
	for _, field := range []string{"admission_rules", "volunteers", "capacity"} {
		var payload map[string]any
		json.Unmarshal(raw, &payload)
		payload["scope"].(map[string]any)[field] = []string{}
		data, _ := json.Marshal(payload)
		if Validate("request", data) == nil {
			t.Fatalf("accepted %s", field)
		}
	}
}
