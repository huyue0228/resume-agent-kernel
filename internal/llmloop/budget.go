package llmloop

import (
	"resume-agent-kernel/internal/model"
	p "resume-agent-kernel/internal/protocol"
)

type StopError struct{ Reason string }

func (e *StopError) Error() string { return "analysis stopped: " + e.Reason }
func stop(trace *p.SafeTrace, reason string) error {
	trace.Budget.StopReason = reason
	return &StopError{reason}
}

func initBudget(b p.TaskBudgetV1, t *p.SafeTrace) p.TaskBudgetV1 {
	if b.MaxContextTokens == 0 {
		b.MaxContextTokens = 32768
	}
	t.Budget = &p.BudgetTrace{MaxTokens: b.MaxTokens, MaxContextTokens: b.MaxContextTokens, MaxTurns: b.MaxTurns, MaxToolCalls: b.MaxToolCalls, RemainingTokens: max(0, b.MaxTokens-t.InputTokens-t.OutputTokens), Tokenizer: "cl100k_base_calibrated", UsageSource: "estimated"}
	return b
}
func recordUsage(counter *model.TokenCounter, messages []model.Message, content string, usage model.Usage, outputLimit int) (int, int, string) {
	input, output := usage.InputTokens, usage.OutputTokens
	inputKnown := usage.InputReported || input > 0
	outputKnown := usage.OutputReported || output > 0
	unknownInput, unknownOutput := usage.UnknownInputAttempts, usage.UnknownOutputAttempts
	if usage.Attempts == 0 {
		if !inputKnown {
			unknownInput = 1
		}
		if !outputKnown {
			unknownOutput = 1
		}
	}
	if inputKnown && unknownInput == 0 {
		counter.Observe(messages, input)
	}
	input += unknownInput * counter.Estimate(messages)
	if unknownOutput > 0 {
		// Earlier transport attempts have unknown output; reserve their full
		// caps. For a returned response without usage, tokenize its actual text.
		if !outputKnown && content != "" {
			output += counter.Text(content)
			unknownOutput--
		}
		output += unknownOutput * outputLimit
	}
	source := "reported"
	if !inputKnown && !outputKnown {
		source = "estimated"
	} else if usage.UnknownInputAttempts > 0 || usage.UnknownOutputAttempts > 0 || !inputKnown || !outputKnown {
		source = "mixed"
	}
	return input, output, source
}
func mergeUsageSource(rounds []p.RoundTrace) string {
	source := ""
	for _, r := range rounds {
		if source == "" {
			source = r.UsageSource
		} else if source != r.UsageSource {
			return "mixed"
		}
	}
	if source == "" {
		return "estimated"
	}
	return source
}
func phaseName(final bool) string {
	if final {
		return "finalize"
	}
	return "analysis"
}
