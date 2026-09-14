package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	c "resume-agent-kernel/internal/contract"
)

type allocationEvaluator interface {
	AllocationCapabilities() c.AllocationCapabilities
	ExecuteAllocation(context.Context, c.AllocationRequest) (c.AllocationResponse, error)
}

func (h *Handler) allocationAuth(w http.ResponseWriter, r *http.Request) (allocationEvaluator, bool) {
	if h.token == "" || !constantTimeEqual(r.Header.Get("X-Agent-Kernel-Token"), h.token) {
		writeError(w, 401, "kernel_unauthorized", "Agent Kernel authentication failed")
		return nil, false
	}
	e, ok := h.evaluator.(allocationEvaluator)
	if !ok {
		writeError(w, 503, "kernel_unavailable", "Allocation unavailable")
	}
	return e, ok
}
func (h *Handler) allocationCapabilities(w http.ResponseWriter, r *http.Request) {
	e, ok := h.allocationAuth(w, r)
	if !ok {
		return
	}
	value := e.AllocationCapabilities()
	raw, _ := json.Marshal(value)
	if c.Validate("allocation.capabilities", raw) != nil {
		writeError(w, 503, "kernel_unavailable", "Allocation capabilities invalid")
		return
	}
	writeJSON(w, 200, value)
}
func (h *Handler) executeAllocation(w http.ResponseWriter, r *http.Request) {
	e, ok := h.allocationAuth(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	raw, err := io.ReadAll(r.Body)
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		writeError(w, 413, "request_too_large", "Encoded request exceeds 2 MiB")
		return
	}
	if err != nil || !json.Valid(raw) {
		writeError(w, 400, "invalid_envelope", "Invalid allocation JSON")
		return
	}
	if c.Validate("allocation.request", raw) != nil {
		writeError(w, 422, "invalid_envelope", "Allocation contract invalid")
		return
	}
	var req c.AllocationRequest
	if json.Unmarshal(raw, &req) != nil {
		writeError(w, 422, "invalid_envelope", "Allocation contract invalid")
		return
	}
	allocationDefaults(&req)
	result, err := e.ExecuteAllocation(r.Context(), req)
	if err != nil {
		code, status := "allocation_output_invalid", 422
		switch err.Error() {
		case "kernel_version_unavailable", "idempotency_conflict", "allocation_snapshot_stale":
			code, status = err.Error(), 409
		case "invalid_envelope", "allocation_snapshot_invalid", "agent_budget_exhausted":
			code = err.Error()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code, status = "allocation_timeout", 504
		}
		if errors.Is(err, context.Canceled) {
			code, status = "allocation_cancelled", 408
		}
		writeError(w, status, code, "分配任务未执行，请查看原因码后重试")
		return
	}
	writeJSON(w, 200, result)
}

// Apply the defaults published by the request schema, after raw JSON validation.
func allocationDefaults(r *c.AllocationRequest) {
	if r.ProtocolVersion == "" {
		r.ProtocolVersion = "resume-allocation/v1"
	}
	if r.TaskKind == "" {
		r.TaskKind = "pool.candidate_allocation"
	}
	if r.ExecutionMode == "" {
		r.ExecutionMode = "deterministic"
	}
	if r.Pin.ProtocolVersion == "" {
		r.Pin.ProtocolVersion = "resume-allocation/v1"
	}
	if r.Pin.ResultSchemaVersion == "" {
		r.Pin.ResultSchemaVersion = "resume-allocation-plan/v1"
	}
	if r.Pin.PolicyVersion == "" {
		r.Pin.PolicyVersion = "allocation-order/v1"
	}
	if r.Pin.InstructionVersion == "" {
		r.Pin.InstructionVersion = "allocation-deterministic/v1"
	}
	if r.Snapshot.WindowSeconds == 0 {
		r.Snapshot.WindowSeconds = 604800
	}
	if r.Budget.MaxDurationSeconds == 0 {
		r.Budget.MaxDurationSeconds = 30
	}
	if r.Budget.MaxToolCalls == 0 {
		r.Budget.MaxToolCalls = 16
	}
}
