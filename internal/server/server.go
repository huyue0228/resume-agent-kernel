package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"resume-agent-kernel/internal/contract"
	"resume-agent-kernel/internal/protocol"
)

const maxRequestBytes = protocol.MaxRequestBytes

type Evaluator interface {
	ExecuteAnalysis(context.Context, protocol.AnalysisRequestV5, string) (protocol.AnalysisResponseV5, error)
	Capabilities() (protocol.KernelCapabilitiesV1, error)
}

type Handler struct {
	evaluator Evaluator
	token     string
	logger    *slog.Logger
	mux       *http.ServeMux
}

func New(evaluator Evaluator, token string, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	handler := &Handler{evaluator: evaluator, token: token, logger: logger, mux: http.NewServeMux()}
	handler.mux.HandleFunc("GET /v2/allocation/capabilities", handler.allocationCapabilities)
	handler.mux.HandleFunc("POST /v2/allocation/tasks/execute", handler.executeAllocation)
	handler.mux.HandleFunc("GET /healthz", handler.health)
	handler.mux.HandleFunc("GET /v2/capabilities", handler.capabilities)
	handler.mux.HandleFunc("POST /v2/tasks/execute", handler.executeTask)
	return handler.withRecovery(handler.mux)
}

func (h *Handler) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
}
func (h *Handler) capabilities(w http.ResponseWriter, r *http.Request) {
	if h.token == "" || !constantTimeEqual(r.Header.Get("X-Agent-Kernel-Token"), h.token) {
		writeError(w, 401, "kernel_unauthorized", "Agent Kernel authentication failed")
		return
	}
	value, err := h.evaluator.Capabilities()
	if err != nil {
		writeError(w, 503, "kernel_unavailable", "Kernel capabilities unavailable")
		return
	}
	raw, err := json.Marshal(value)
	if err != nil || contract.Validate("capabilities", raw) != nil {
		writeError(w, 503, "kernel_unavailable", "Kernel capabilities invalid")
		return
	}
	writeJSON(w, 200, value)
}

func (h *Handler) executeTask(w http.ResponseWriter, r *http.Request) {
	if h.token == "" || !constantTimeEqual(r.Header.Get("X-Agent-Kernel-Token"), h.token) {
		writeError(w, 401, "kernel_unauthorized", "Agent Kernel authentication failed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	raw, readErr := io.ReadAll(r.Body)
	var tooLarge *http.MaxBytesError
	if errors.As(readErr, &tooLarge) {
		writeError(w, 413, "request_too_large", "Encoded request exceeds 2 MiB")
		return
	}
	if readErr != nil || !json.Valid(raw) {
		writeError(w, 400, "invalid_envelope", "Invalid analysis request JSON")
		return
	}
	var header struct {
		ProtocolVersion string `json:"protocol_version"`
		Pin             struct {
			ProtocolVersion string `json:"protocol_version"`
		} `json:"pin"`
	}
	if json.Unmarshal(raw, &header) != nil || header.ProtocolVersion != protocol.TaskProtocolVersion || header.Pin.ProtocolVersion != protocol.TaskProtocolVersion {
		writeError(w, 409, "agent_protocol_incompatible", "Expected resume-analysis/v5")
		return
	}
	if err := contract.Validate("request", raw); err != nil {
		writeError(w, 422, "invalid_envelope", "Analysis request contract invalid")
		return
	}
	var e protocol.AnalysisRequestV5
	if json.Unmarshal(raw, &e) != nil {
		writeError(w, 400, "invalid_envelope", "Invalid analysis request")
		return
	}
	started := time.Now()
	result, err := h.evaluator.ExecuteAnalysis(r.Context(), e, r.Header.Get("X-Model-API-Key"))
	if err != nil {
		code, status := safeError(err)
		h.logger.Warn("analysis rejected", "task_id", e.TaskID, "code", code, "duration_ms", time.Since(started).Milliseconds())
		if strings.Contains(err.Error(), "idempotency") {
			code = "idempotency_conflict"
			status = 409
		}
		writeError(w, status, code, publicMessage(code))
		return
	}
	h.logger.Info("analysis completed", "task_id", e.TaskID, "trace_id", result.Trace.TraceID, "status", result.Manifest.TerminalState, "duration_ms", time.Since(started).Milliseconds())
	writeJSON(w, 200, result)
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
	case errors.Is(err, protocol.ErrVersionUnavailable):
		return "kernel_version_unavailable", http.StatusConflict
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
	case "kernel_version_unavailable":
		return "冻结的 Kernel 版本已不可用，请重新提交任务"
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
