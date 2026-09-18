package protocol

import (
	"errors"
	"math"
	"time"
)

type ModelConfig struct {
	APIStyle           string  `json:"api_style"`
	BaseURL            string  `json:"base_url"`
	ModelName          string  `json:"model_name"`
	StructuredOutput   string  `json:"structured_output_mode"`
	TimeoutSeconds     float64 `json:"timeout_seconds"`
	RetryCount         int     `json:"retry_count"`
	InsecureSkipVerify bool    `json:"insecure_skip_verify"`
}

type ScoreBreakdown struct {
	MajorMatch         float64 `json:"major_match"`
	SkillsMatch        float64 `json:"skills_match"`
	ExperienceEvidence float64 `json:"experience_evidence"`
	JobRequirement     float64 `json:"job_requirement"`
	ResumeQuality      float64 `json:"resume_quality"`
}

type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolTrace struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	ItemCount  int    `json:"item_count"`
	ErrorCode  string `json:"error_code,omitempty"`
	ErrorField string `json:"error_field,omitempty"`
	Repeated   bool   `json:"repeated,omitempty"`
}

type RoundTrace struct {
	Turn                 int    `json:"turn"`
	Phase                string `json:"phase"`
	EstimatedInputTokens int    `json:"estimated_input_tokens"`
	InputTokens          int    `json:"input_tokens"`
	OutputTokens         int    `json:"output_tokens"`
	OutputLimit          int    `json:"output_limit"`
	RemainingTokens      int    `json:"remaining_tokens"`
	ReservedTokens       int    `json:"reserved_tokens"`
	ModelDurationMS      int64  `json:"model_duration_ms"`
	UsageSource          string `json:"usage_source"`
	TransportAttempts    int    `json:"transport_attempts"`
	Progress             bool   `json:"progress"`
	Compacted            bool   `json:"compacted"`
}

// BudgetTrace contains counters and controlled codes only, never prompts or tool data.
type BudgetTrace struct {
	MaxTokens          int    `json:"max_tokens"`
	MaxContextTokens   int    `json:"max_context_tokens"`
	MaxTurns           int    `json:"max_turns"`
	MaxToolCalls       int    `json:"max_tool_calls"`
	RemainingTokens    int    `json:"remaining_tokens"`
	NextInputTokens    int    `json:"next_input_tokens"`
	ReservedTokens     int    `json:"reserved_tokens"`
	StopReason         string `json:"stop_reason"`
	Tokenizer          string `json:"tokenizer"`
	UsageSource        string `json:"usage_source"`
	Compactions        int    `json:"compactions"`
	RepeatedCalls      int    `json:"repeated_calls"`
	FormatRepairs      int    `json:"format_repairs"`
	ValidationFailures int    `json:"validation_failures"`
	TransportRetries   int    `json:"transport_retries"`
}

type SafeTrace struct {
	TraceID       string       `json:"trace_id"`
	KernelBuild   string       `json:"kernel_build"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    time.Time    `json:"finished_at"`
	Turns         int          `json:"turns"`
	ToolCallCount int          `json:"tool_call_count"`
	ToolCalls     []ToolTrace  `json:"tool_calls"`
	InputTokens   int          `json:"input_tokens"`
	OutputTokens  int          `json:"output_tokens"`
	Status        string       `json:"status"`
	Budget        *BudgetTrace `json:"budget,omitempty"`
	Rounds        []RoundTrace `json:"rounds,omitempty"`
}

func (s ScoreBreakdown) Validate() error {
	for _, value := range []float64{s.MajorMatch, s.SkillsMatch, s.ExperienceEvidence, s.JobRequirement, s.ResumeQuality} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return errors.New("score breakdown values must be finite and between 0 and 1")
		}
	}
	return nil
}
