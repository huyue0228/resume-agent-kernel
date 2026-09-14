package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"resume-agent-kernel/internal/allocation"
	c "resume-agent-kernel/internal/contract"
	"resume-agent-kernel/internal/runtime"
	"testing"
	"time"
)

func TestAllocationHTTPIsolationAndDefaults(t *testing.T) {
	h := New(runtime.NewService("dev"), "allocation-http-test", nil)
	raw, _ := c.Bundle.ReadFile("bundle/allocation.request.example.json")
	var req c.AllocationRequest
	_ = json.Unmarshal(raw, &req)
	req.Snapshot.SnapshotAt = time.Now().UTC().Format(time.RFC3339)
	req.SnapshotHash, _ = allocation.SnapshotHash(req.Snapshot)
	raw, _ = json.Marshal(req)
	call := func(method, path, token string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("X-Agent-Kernel-Token", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if got := call("GET", "/v2/allocation/capabilities", "", nil); got.Code != http.StatusUnauthorized {
		t.Fatal("unauthorized capabilities exposed")
	}
	if got := call("GET", "/v2/allocation/capabilities", "allocation-http-test", nil); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	delete(body, "budget")
	encoded, _ := json.Marshal(body)
	if got := call("POST", "/v2/allocation/tasks/execute", "allocation-http-test", encoded); got.Code != 200 {
		t.Fatal("published defaults rejected", got.Body.String())
	}
	for _, key := range []string{"resume_text", "quote", "file_url", "model", "summary", "headcount", "is_public"} {
		body[key] = "forbidden"
		b, _ := json.Marshal(body)
		got := call("POST", "/v2/allocation/tasks/execute", "allocation-http-test", b)
		if got.Code != 422 {
			t.Fatalf("accepted forbidden %s", key)
		}
		delete(body, key)
	}
	if got := call("POST", "/v2/allocation/tasks/execute", "allocation-http-test", bytes.Repeat([]byte(" "), (2<<20)+1)); got.Code != 413 {
		t.Fatal("size limit not enforced")
	}
}
