package llmloop

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"resume-agent-kernel/internal/agent"
	"resume-agent-kernel/internal/model"
	p "resume-agent-kernel/internal/protocol"
	"resume-agent-kernel/internal/tools"
)

type conversation struct {
	rounds     [][]model.Message
	seen       map[string]bool
	read       map[int]bool
	noProgress int
}

func newConversation() *conversation {
	return &conversation{seen: map[string]bool{}, read: map[int]bool{}}
}
func callKey(call p.ToolCall) string {
	raw, _ := json.Marshal([]any{call.Name, call.Arguments})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func finalTool(name string) bool {
	switch name {
	case "candidate_profile.submit", "job_match.submit", "task_done", "evidence.verify_quotes", "resume.read_sections":
		return true
	}
	return false
}
func (h *conversation) observe(call p.ToolCall, raw []byte, err error, before int, c *agent.Collector) (bool, bool) {
	key := callKey(call)
	repeated := h.seen[key]
	h.seen[key] = true
	if err != nil {
		return false, repeated
	}
	if call.Name == "resume.read_sections" || call.Name == "resume.search_evidence" {
		var lines []agent.Line
		_ = json.Unmarshal(raw, &lines)
		progress := false
		resident := h.residentLines()
		for _, line := range lines {
			if line.Number >= 1 && line.Number <= len(c.Lines) && (!h.read[line.Number] || !resident[line.Number]) {
				h.read[line.Number] = true
				progress = true
			}
		}
		return progress, repeated
	}
	if call.Name == "candidate_profile.submit" || call.Name == "job_match.submit" || call.Name == "task_done" {
		return collectorProgress(c) > before, repeated
	}
	return !repeated, repeated
}

// Reading a previously compacted-away source is useful work. Do not count it
// as a stall merely because that page was read earlier in the task.
func (h *conversation) residentLines() map[int]bool {
	resident := map[int]bool{}
	for _, round := range h.rounds {
		for _, message := range round {
			if message.Role != "user" {
				continue
			}
			var payload struct {
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal([]byte(message.Content), &payload) != nil {
				continue
			}
			var lines []agent.Line
			if json.Unmarshal(payload.Result, &lines) != nil {
				continue
			}
			for _, line := range lines {
				resident[line.Number] = true
			}
		}
	}
	return resident
}
func collectorProgress(c *agent.Collector) int {
	n := len(c.Matches)
	if c.Profile != nil {
		n++
	}
	if c.Done {
		n++
	}
	return n
}
func (h *conversation) ranges() [][2]int {
	lines := make([]int, 0, len(h.read))
	for line := range h.read {
		lines = append(lines, line)
	}
	sort.Ints(lines)
	ranges := [][2]int{}
	for _, line := range lines {
		if len(ranges) > 0 && ranges[len(ranges)-1][1]+1 == line {
			ranges[len(ranges)-1][1] = line
		} else {
			ranges = append(ranges, [2]int{line, line})
		}
	}
	return ranges
}
func (h *conversation) messages(registry *tools.Registry, c *agent.Collector, b p.TaskBudgetV1, t *p.SafeTrace, final bool) []model.Message {
	defs := registry.Catalog()
	if final {
		filtered := []tools.Definition{}
		for _, def := range defs {
			if finalTool(def.Name) {
				filtered = append(filtered, def)
			}
		}
		defs = filtered
	}
	jobs := []p.JobSnapshotV1{}
	refs := []string{}
	for ref := range c.Jobs {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	missing := []string{}
	if c.Profile == nil {
		missing = append(missing, "candidate_profile.submit")
	}
	for _, ref := range refs {
		jobs = append(jobs, c.Jobs[ref])
		if _, ok := c.Matches[ref]; !ok {
			missing = append(missing, "job_match.submit")
		}
	}
	missing = append(missing, "task_done")
	sections := [][3]int{}
	for _, line := range c.Lines {
		if len(sections) > 0 && sections[len(sections)-1][0] == line.Page {
			sections[len(sections)-1][2] = line.Number
		} else {
			sections = append(sections, [3]int{line.Page, line.Number, line.Number})
		}
	}
	state := map[string]any{"phase": "analysis", "remaining_tokens": max(0, b.MaxTokens-t.InputTokens-t.OutputTokens), "remaining_turns": b.MaxTurns - t.Turns, "remaining_tool_calls": b.MaxToolCalls - t.ToolCallCount, "missing": missing, "read_line_ranges": h.ranges(), "consecutive_no_progress": h.noProgress, "verified_profile": c.Profile, "verified_matches": c.Ranked()}
	if final {
		state["phase"] = "finalize"
	}
	// Only code-verified state is persisted across compaction. Original lines
	// remain in Collector and are always available to read_sections.
	catalog, _ := json.Marshal(map[string]any{"available_tools": defs, "budget": b, "runtime_state": state, "candidate": c.Candidate, "tag_catalog": c.TagCatalog, "jobs": jobs, "resume_sections_page_start_end": sections, "profile_already_submitted": c.Profile != nil})
	system := instructions
	if final {
		system += "\n运行时已进入收尾阶段：只修正必要证据、提交尚未成功的画像和岗位分析，然后 task_done。不得发起新的广泛搜索；无法完成请明确 FAILED。"
	}
	messages := []model.Message{{Role: "system", Content: system}, {Role: "user", Content: string(catalog)}}
	for _, round := range h.rounds {
		messages = append(messages, round...)
	}
	return messages
}
func (h *conversation) compact(keep int) bool {
	if len(h.rounds) <= keep {
		return false
	}
	h.rounds = append([][]model.Message(nil), h.rounds[len(h.rounds)-keep:]...)
	return true
}
