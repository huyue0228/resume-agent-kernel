package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"resume-agent-kernel/internal/contract"
	"resume-agent-kernel/internal/protocol"
)

const maxRequestBytes = 2 << 20

type Evaluator interface {
	Evaluate(context.Context, protocol.CaseEnvelopeV2, string) (protocol.AgentActionProposalV1, error)
}

type Handler struct {
	evaluator Evaluator
	token     string
	build     string
	logger    *slog.Logger
	mux       *http.ServeMux
}

func New(evaluator Evaluator, token, build string, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	handler := &Handler{evaluator: evaluator, token: token, build: build, logger: logger, mux: http.NewServeMux()}
	handler.mux.HandleFunc("GET /healthz", handler.health)
	handler.mux.HandleFunc("POST /v1/evaluate", handler.evaluate)
	handler.mux.HandleFunc("POST /v2/tasks/execute", handler.executeTask)
	return handler.withRecovery(handler.mux)
}

func (h *Handler) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok":                    true,
		"task_protocol_version": protocol.TaskProtocolVersion,
		"task_toolset_version":  protocol.MatchToolsetVersion,
		"task_result_version":   protocol.TaskResultVersion,
		"build":                 h.build,
		"protocol_version":      protocol.ProtocolVersion,
		"toolset_version":       protocol.ToolsetVersion,
		"result_schema_version": protocol.ResultVersion,
		"instruction_version":   protocol.InstructionVersion,
	})
}

func (h *Handler) executeTask(w http.ResponseWriter, r *http.Request) {
	if h.token == "" || !constantTimeEqual(r.Header.Get("X-Agent-Kernel-Token"), h.token) {
		writeError(w, 401, "kernel_unauthorized", "Agent Kernel authentication failed")
		return
	}
	executor, ok := h.evaluator.(interface {
		ExecuteAnalysis(context.Context, protocol.AnalysisRequestV1, string) (protocol.AnalysisResponseV1, error)
	})
	if !ok {
		writeError(w, 503, "kernel_unavailable", "Task runtime unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var raw json.RawMessage
	if decoder.Decode(&raw) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid_envelope", "invalid TaskEnvelopeV1")
		return
	}
	if err := contract.Validate("request", raw); err != nil {
		writeError(w, 422, "invalid_envelope", "Analysis request contract invalid")
		return
	}
	var e protocol.AnalysisRequestV1
	if json.Unmarshal(raw, &e) != nil {
		writeError(w, 400, "invalid_envelope", "Invalid analysis request")
		return
	}
	result, err := executor.ExecuteAnalysis(r.Context(), e, r.Header.Get("X-Model-API-Key"))
	if err != nil {
		code, status := safeError(err)
		if strings.Contains(err.Error(), "idempotency") {
			code = "idempotency_conflict"
			status = 409
		}
		writeError(w, status, code, publicMessage(code))
		return
	}
	writeJSON(w, 200, result)
}

func (h *Handler) evaluate(writer http.ResponseWriter, request *http.Request) {
	if h.token == "" || !constantTimeEqual(request.Header.Get("X-Agent-Kernel-Token"), h.token) {
		writeError(writer, http.StatusUnauthorized, "kernel_unauthorized", "Agent Kernel authentication failed")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var envelope protocol.CaseEnvelopeV2
	if err := decoder.Decode(&envelope); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_envelope", "invalid CaseEnvelopeV2")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_envelope", "invalid CaseEnvelopeV2")
		return
	}
	if err := envelope.Validate(); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_envelope", err.Error())
		return
	}
	started := time.Now()
	proposal, err := h.evaluator.Evaluate(request.Context(), envelope, request.Header.Get("X-Model-API-Key"))
	if err != nil {
		code, status := safeError(err)
		h.logger.Warn("agent evaluation failed", "task_id", envelope.TaskID, "code", code, "duration_ms", time.Since(started).Milliseconds(), "error_type", errorType(err))
		payload := map[string]any{"ok": false, "code": code, "detail": publicMessage(code)}
		var evaluationError *protocol.EvaluationError
		if errors.As(err, &evaluationError) {
			payload["safe_trace"] = evaluationError.Trace
		}
		writeJSONStatus(writer, status, payload)
		return
	}
	h.logger.Info("agent evaluation completed", "task_id", envelope.TaskID, "trace_id", proposal.Trace.TraceID, "turns", proposal.Trace.Turns, "tool_calls", proposal.Trace.ToolCallCount, "duration_ms", time.Since(started).Milliseconds())
	writeJSON(writer, http.StatusOK, proposal)
}

func (h *Handler) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				h.logger.Error("agent kernel panic recovered", "path", request.URL.Path)
				writeError(writer, http.StatusInternalServerError, "kernel_internal_error", "Agent Kernel internal error")
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func safeError(err error) (string, int) {
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout"):
		return "llm_timeout", http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return "agent_cancelled", http.StatusRequestTimeout
	case strings.Contains(message, "budget"):
		return "agent_budget_exhausted", http.StatusUnprocessableEntity
	case strings.Contains(message, "evidence"):
		return "agent_evidence_invalid", http.StatusUnprocessableEntity
	case strings.Contains(message, "model"):
		return "ai_connection_error", http.StatusBadGateway
	default:
		return "agent_invalid_output", http.StatusUnprocessableEntity
	}
}

func publicMessage(code string) string {
	switch code {
	case "llm_timeout":
		return "模型请求超时"
	case "agent_cancelled":
		return "Agent 任务已取消"
	case "agent_budget_exhausted":
		return "Agent 已达到本次工具或轮次预算"
	case "agent_evidence_invalid":
		return "Agent 返回的简历证据无法校验"
	case "ai_connection_error":
		return "模型服务连接失败"
	default:
		return "Agent 未返回符合协议的结果"
	}
}

func errorType(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T", err)
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSONStatus(writer, status, map[string]any{"ok": false, "code": code, "detail": message})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writeJSONStatus(writer, status, value)
}

func writeJSONStatus(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
