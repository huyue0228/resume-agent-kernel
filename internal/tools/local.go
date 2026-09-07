package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"resume-agent-kernel/internal/protocol"
)

type CaseProvider struct {
	envelope protocol.CaseEnvelopeV2
}

func NewCaseProvider(envelope protocol.CaseEnvelopeV2) *CaseProvider {
	return &CaseProvider{envelope: envelope}
}

func (*CaseProvider) Name() string { return "case-envelope" }

func (*CaseProvider) Definitions() []Definition {
	emptyObject := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
	return []Definition{
		{Name: "case.read_constraints", Description: "读取已由 Django 固定的流程约束、候选人引用和当前志愿。", InputSchema: emptyObject, ReadOnly: true},
		{Name: "job.read_fixed_context", Description: "读取当前志愿对应的固定岗位与部门上下文。", InputSchema: emptyObject, ReadOnly: true},
		{Name: "resume.read_sections", Description: "按行读取简历正文片段。", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"start_line": map[string]any{"type": "integer", "minimum": 0}, "max_lines": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}}, "additionalProperties": false}, ReadOnly: true},
		{Name: "resume.search_evidence", Description: "在简历正文中搜索关键词并返回附近原文。", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}}, "required": []string{"query"}, "additionalProperties": false}, ReadOnly: true},
		{Name: "evidence.verify_quotes", Description: "逐条校验引用是否存在于简历正文。", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"quotes": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}}}, "required": []string{"quotes"}, "additionalProperties": false}, ReadOnly: true},
	}
}

func (p *CaseProvider) Execute(_ context.Context, call protocol.ToolCall) (Result, error) {
	var value any
	var count int
	switch call.Name {
	case "case.read_constraints":
		if err := validateArgumentKeys(call.Arguments); err != nil {
			return Result{}, err
		}
		value = map[string]any{
			"constraints":         p.envelope.Constraints,
			"candidate_reference": p.envelope.Candidate,
			"current_volunteer":   p.envelope.CurrentVolunteer,
		}
		count = 1
	case "job.read_fixed_context":
		if err := validateArgumentKeys(call.Arguments); err != nil {
			return Result{}, err
		}
		value = p.envelope.CurrentJob
		count = 1
	case "resume.read_sections":
		if err := validateArgumentKeys(call.Arguments, "start_line", "max_lines"); err != nil {
			return Result{}, err
		}
		start, err := integerArg(call.Arguments, "start_line", 0)
		if err != nil || start < 0 {
			return Result{}, errors.New("start_line must be a non-negative integer")
		}
		limit, err := integerArg(call.Arguments, "max_lines", 120)
		if err != nil || limit < 1 || limit > 200 {
			return Result{}, errors.New("max_lines must be between 1 and 200")
		}
		lines := strings.Split(p.envelope.Resume.Text, "\n")
		if start > len(lines) {
			start = len(lines)
		}
		end := min(start+limit, len(lines))
		selected := lines[start:end]
		value = map[string]any{"start_line": start, "next_line": end, "total_lines": len(lines), "lines": selected}
		count = len(selected)
	case "resume.search_evidence":
		if err := validateArgumentKeys(call.Arguments, "query"); err != nil {
			return Result{}, err
		}
		query, _ := call.Arguments["query"].(string)
		query = strings.TrimSpace(query)
		if query == "" || len([]rune(query)) > 256 {
			return Result{}, errors.New("query must contain 1 to 256 characters")
		}
		matches := searchWindows(p.envelope.Resume.Text, query, 5)
		value = map[string]any{"query": query, "matches": matches}
		count = len(matches)
	case "evidence.verify_quotes":
		if err := validateArgumentKeys(call.Arguments, "quotes"); err != nil {
			return Result{}, err
		}
		rawQuotes, ok := call.Arguments["quotes"].([]any)
		if !ok || len(rawQuotes) == 0 || len(rawQuotes) > 20 {
			return Result{}, errors.New("quotes must be an array containing 1 to 20 strings")
		}
		items := make([]map[string]any, 0, len(rawQuotes))
		verified := 0
		for _, raw := range rawQuotes {
			quote, ok := raw.(string)
			if !ok || strings.TrimSpace(quote) == "" || len([]rune(quote)) > 500 {
				return Result{}, errors.New("each quote must contain 1 to 500 characters")
			}
			found := ContainsNormalized(p.envelope.Resume.Text, quote)
			if found {
				verified++
			}
			items = append(items, map[string]any{"quote": quote, "verified": found})
		}
		value = map[string]any{"items": items, "all_verified": verified == len(items)}
		count = len(items)
	default:
		return Result{}, fmt.Errorf("case provider does not implement tool %q", call.Name)
	}
	payload, err := json.Marshal(value)
	return Result{Payload: payload, ItemCount: count}, err
}

func validateArgumentKeys(args map[string]any, allowedKeys ...string) error {
	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = struct{}{}
	}
	for key := range args {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unexpected tool argument %q", key)
		}
	}
	return nil
}

func integerArg(args map[string]any, key string, fallback int) (int, error) {
	raw, ok := args[key]
	if !ok {
		return fallback, nil
	}
	value, ok := raw.(float64)
	if !ok || value != float64(int(value)) {
		return 0, errors.New("not an integer")
	}
	return int(value), nil
}

func normalize(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, value)
}

func ContainsNormalized(text, quote string) bool {
	needle := normalize(quote)
	return len([]rune(needle)) >= 4 && strings.Contains(normalize(text), needle)
}

func searchWindows(text, query string, limit int) []map[string]any {
	textRunes := []rune(text)
	queryRunes := []rune(query)
	if len(queryRunes) == 0 {
		return nil
	}
	lowerText := []rune(strings.ToLower(text))
	lowerQuery := []rune(strings.ToLower(query))
	results := make([]map[string]any, 0, limit)
	for index := 0; index+len(lowerQuery) <= len(lowerText) && len(results) < limit; index++ {
		if string(lowerText[index:index+len(lowerQuery)]) != string(lowerQuery) {
			continue
		}
		start := max(index-80, 0)
		end := min(index+len(queryRunes)+80, len(textRunes))
		results = append(results, map[string]any{"offset": index, "text": string(textRunes[start:end])})
		index += len(lowerQuery) - 1
	}
	return results
}
