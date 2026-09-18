package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	p "resume-agent-kernel/internal/protocol"
)

func TestOfflineTokenizerCountsTextInsteadOfUTF8Bytes(t *testing.T) {
	c, err := NewTokenCounter()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("负责后端服务开发与测试工作", 100)
	if c.Text("hello world") != 2 || c.Text(text) >= len(text)/2 {
		t.Fatal("counter regressed to byte-based estimation")
	}
	if _, err := (embeddedLoader{}).LoadTiktokenBpe("https://invalid.example/unknown.tiktoken"); err == nil {
		t.Fatal("unbundled encoding accepted")
	}
	messages := []Message{{Role: "user", Content: text}}
	before := c.Estimate(messages)
	c.Observe(messages, c.Base(messages)/2)
	if c.Estimate(messages) >= before {
		t.Fatal("reported usage did not calibrate another model's tokenizer")
	}
	modern, err := NewTokenCounterForModel("gpt-4o")
	if err != nil || modern.Name() != "o200k_base_calibrated" || modern.Text(text) >= len(text)/2 {
		t.Fatal("modern offline vocabulary was not selected")
	}
}
func TestInvalidAndEmptyModelOutputsStillAccountForUsage(t *testing.T) {
	for _, style := range []string{"chat_json", "responses"} {
		t.Run(style, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				key := "max_completion_tokens"
				if style == "responses" {
					key = "max_output_tokens"
				}
				if body[key] != float64(2048) {
					t.Error("request omitted output cap")
				}
				if style == "responses" {
					_, _ = w.Write([]byte(`{"output":[],"usage":{"input_tokens":1200,"output_tokens":70}}`))
				} else {
					_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""}}],"usage":{"prompt_tokens":1200,"completion_tokens":70}}`))
				}
			}))
			defer server.Close()
			client, err := NewHTTPClient(p.ModelConfig{BaseURL: server.URL, APIStyle: style, ModelName: "fake", TimeoutSeconds: 1}, "")
			if err != nil {
				t.Fatal(err)
			}
			client.OutputBudget = 2048
			_, usage, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "synthetic"}})
			if !errors.Is(err, ErrInvalidResponse) || usage.InputTokens != 1200 || usage.OutputTokens != 70 || !usage.InputReported || usage.Attempts != 1 {
				t.Fatalf("invalid output lost usage: %+v %v", usage, err)
			}
		})
	}
}
func TestTransportRetriesAreVisibleAndMissingUsageIsNotZero(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":321,"completion_tokens":0}}`))
	}))
	defer server.Close()
	client, _ := NewHTTPClient(p.ModelConfig{BaseURL: server.URL, APIStyle: "chat_json", TimeoutSeconds: 1, RetryCount: 1}, "")
	_, u, err := client.Complete(context.Background(), nil)
	if err != nil || u.Attempts != 2 || u.UnknownInputAttempts != 1 || u.UnknownOutputAttempts != 1 || u.InputTokens != 321 || !u.OutputReported {
		t.Fatalf("incorrect retry accounting: %+v %v", u, err)
	}
	_, missing, err := parseChat([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	if err != nil || missing.InputReported || missing.OutputReported {
		t.Fatal("missing usage treated as reported zero")
	}
}
