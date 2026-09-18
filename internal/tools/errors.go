package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ValidationError is safe to send to the model and trace. No submitted values,
// quotes, provider response bodies or credentials may be included.
type ValidationError struct {
	Code  string `json:"code"`
	Field string `json:"field"`
	Hint  string `json:"hint"`
}

func (e *ValidationError) Error() string     { return e.Code + ": " + e.Field + ": " + e.Hint }
func Invalid(code, field, hint string) error { return &ValidationError{code, field, hint} }
func Feedback(err error) *ValidationError {
	var v *ValidationError
	if errors.As(err, &v) {
		return v
	}
	return &ValidationError{"tool_failed", "arguments", "工具未完成；请检查参数和允许范围，不要原样重复失败的调用。"}
}

// Find the smallest failing schema location without exposing the validator's
// error string, which can contain the submitted resume text.
func schemaFeedback(schema any, value any) error {
	raw, _ := json.Marshal(schema)
	var root map[string]any
	_ = json.Unmarshal(raw, &root)
	return locateSchemaIssue(root, value, "arguments")
}
func locateSchemaIssue(s map[string]any, value any, path string) error {
	fail := func(hint string) error { return Invalid("schema_invalid", path, hint) }
	switch s["type"] {
	case "object":
		v, ok := value.(map[string]any)
		if !ok {
			return fail("必须是 JSON 对象。")
		}
		props, _ := s["properties"].(map[string]any)
		if required, ok := s["required"].([]any); ok {
			for _, key := range required {
				if _, exists := v[key.(string)]; !exists {
					return Invalid("schema_invalid", path+"."+key.(string), "缺少必填字段。")
				}
			}
		}
		if s["additionalProperties"] == false {
			for key := range v {
				if _, ok := props[key]; !ok {
					return fail("包含 Schema 未定义的字段；仅保留该工具定义的字段。")
				}
			}
		}
		keys := make([]string, 0, len(props))
		for key := range props {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if child, ok := v[key]; ok {
				property, object := props[key].(map[string]any)
				if !object {
					continue
				}
				if err := locateSchemaIssue(property, child, path+"."+key); err != nil {
					return err
				}
			}
		}
	case "array":
		raw, _ := json.Marshal(value)
		var v []any
		if json.Unmarshal(raw, &v) != nil || v == nil {
			return fail("必须是 JSON 数组。")
		}
		if child, ok := s["items"].(map[string]any); ok {
			for i, item := range v {
				if err := locateSchemaIssue(child, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fail("必须是字符串。")
		}
	case "integer", "number":
		raw, _ := json.Marshal(value)
		var number float64
		if json.Unmarshal(raw, &number) != nil || value == nil {
			return fail("必须是数值。")
		}
		if s["type"] == "integer" && number != float64(int64(number)) {
			return fail("必须是整数。")
		}
		if min, ok := s["minimum"].(float64); ok && number < min {
			return fail(fmt.Sprintf("不得小于 %g。", min))
		}
		if max, ok := s["maximum"].(float64); ok && number > max {
			return fail(fmt.Sprintf("不得大于 %g。", max))
		}
	}
	if values, ok := s["enum"].([]any); ok {
		wanted, _ := json.Marshal(value)
		found := false
		for _, candidate := range values {
			encoded, _ := json.Marshal(candidate)
			if string(encoded) == string(wanted) {
				found = true
			}
		}
		if !found {
			return fail("必须使用 Schema enum 中的允许值。")
		}
	}
	return nil
}
