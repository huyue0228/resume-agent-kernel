package allocation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	c "resume-agent-kernel/internal/contract"
	"slices"
	"sort"
	"sync"
	"time"
)

const Protocol = "resume-allocation/v1"
const Result = "resume-allocation-plan/v1"
const Policy = "allocation-order/v1"
const Toolset = "allocation-tools/v1"
const Instructions = "allocation-deterministic/v1"

var toolNames = []string{"allocation.read_snapshot", "allocation.filter_eligible", "allocation.rank_demands", "allocation.simulate_plan", "allocation.validate_plan", "allocation.submit_plan"}

type entry struct {
	hash   string
	result c.AllocationResponse
	at     time.Time
}
type Service struct {
	build string
	slots chan struct{}
	mu    sync.Mutex
	cache map[string]entry
	now   func() time.Time
}

func New(build string) *Service {
	return &Service{build: build, slots: make(chan struct{}, 4), cache: map[string]entry{}, now: time.Now}
}
func (s *Service) Capabilities() c.AllocationCapabilities {
	return c.AllocationCapabilities{ProtocolVersion: Protocol, ResultSchemaVersion: Result, TaskKinds: []string{"pool.candidate_allocation"}, ExecutionModes: []string{"deterministic"}, KernelBuild: s.build, ToolsetVersion: Toolset, PolicyVersion: Policy, InstructionVersion: Instructions, ToolNames: append([]string{}, toolNames...)}
}

type execution struct {
	request   c.AllocationRequest
	members   []c.AllocationMember
	demands   map[int64]c.AllocationDemand
	options   map[int64][]option
	reasons   map[int64]string
	decisions []c.AllocationDecision
}

func (e *execution) read() error {
	if err := validateSnapshot(e.request.Snapshot); err != nil {
		return err
	}
	e.members = append([]c.AllocationMember{}, e.request.Snapshot.Members...)
	e.demands = map[int64]c.AllocationDemand{}
	for _, d := range e.request.Snapshot.Demands {
		e.demands[d.DemandID] = d
	}
	return nil
}
func (e *execution) filter() error {
	e.options = map[int64][]option{}
	e.reasons = map[int64]string{}
	for _, m := range e.members {
		e.options[m.MemberID], e.reasons[m.MemberID] = eligible(m, e.demands)
	}
	return nil
}
func (e *execution) rank() error {
	sort.Slice(e.members, func(i, j int) bool {
		a, b := e.members[i], e.members[j]
		left, _ := time.Parse(time.RFC3339Nano, a.CreatedAt)
		right, _ := time.Parse(time.RFC3339Nano, b.CreatedAt)
		if !left.Equal(right) {
			return left.Before(right)
		}
		return a.MemberID < b.MemberID
	})
	return nil
}
func (e *execution) simulate() error {
	e.decisions = []c.AllocationDecision{}
	seq := e.request.Snapshot.NextSequence
	for _, m := range e.members {
		options := e.options[m.MemberID]
		for i := range options {
			d := e.demands[options[i].demand.DemandID]
			options[i].order[2] = d.RecentSupplyCount
			options[i].order[3] = d.LastAllocationSequence
		}
		sort.Slice(options, func(i, j int) bool { return less(options[i].order, options[j].order) })
		decision := c.AllocationDecision{MemberID: m.MemberID, QualificationRef: m.QualificationRef, Action: "wait", MatchedTags: []string{}, AssertionRefs: []string{}, Order: []int64{}, ReasonCode: e.reasons[m.MemberID]}
		if len(options) > 0 {
			o := options[0]
			decision.Action = "assign"
			decision.DemandID = o.demand.DemandID
			decision.DepartmentRef = o.demand.DepartmentRef
			decision.MatchedTags = o.hits
			decision.AssertionRefs = o.refs
			decision.Order = append([]int64{}, o.order...)
			decision.Sequence = seq
			decision.ReasonCode = "allocated"
			d := e.demands[o.demand.DemandID]
			if !slices.Contains(m.CountedDemandIDs, d.DemandID) {
				d.RecentSupplyCount++
			}
			d.LastAllocationSequence = seq
			e.demands[d.DemandID] = d
			seq++
		}
		e.decisions = append(e.decisions, decision)
	}
	return nil
}
func (e *execution) validate() error {
	if len(e.decisions) != len(e.members) {
		return errors.New("allocation_output_invalid")
	}
	for i, d := range e.decisions {
		if d.MemberID != e.members[i].MemberID || d.QualificationRef != e.members[i].QualificationRef {
			return errors.New("allocation_output_invalid")
		}
		if d.Action == "assign" && (len(d.Order) != 5 || d.DemandID != d.Order[4]) {
			return errors.New("allocation_output_invalid")
		}
	}
	return nil
}
func (e *execution) submit() error { return nil }
func (s *Service) Execute(ctx context.Context, r c.AllocationRequest) (c.AllocationResponse, error) {
	var zero c.AllocationResponse
	started := s.now()
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 2<<20 || c.Validate("allocation.request", raw) != nil {
		return zero, errors.New("invalid_envelope")
	}
	if r.Pin.KernelBuild != s.build || r.Pin.ToolsetVersion != Toolset || r.Pin.PolicyVersion != Policy || r.Pin.InstructionVersion != Instructions {
		return zero, errors.New("kernel_version_unavailable")
	}
	hash, err := SnapshotHash(r.Snapshot)
	if err != nil || hash != r.SnapshotHash {
		return zero, errors.New("allocation_snapshot_invalid")
	}
	// Normalize set ordering for idempotency without removing any frozen request fields.
	canon, _ := CanonicalSnapshot(r.Snapshot)
	var norm c.AllocationRequest
	_ = json.Unmarshal(raw, &norm)
	_ = json.Unmarshal(canon, &norm.Snapshot)
	b, _ := json.Marshal(norm)
	sum := sha256.Sum256(b)
	inputHash := hex.EncodeToString(sum[:])
	s.mu.Lock()
	cached, ok := s.cache[r.IdempotencyKey]
	s.mu.Unlock()
	if ok && started.Sub(cached.at) < 5*time.Minute {
		if cached.hash != inputHash {
			return zero, errors.New("idempotency_conflict")
		}
		result := cloneResponse(cached.result)
		result.SafeTrace.Reused = true
		return result, nil
	}
	at, err := time.Parse(time.RFC3339, r.Snapshot.SnapshotAt)
	if err != nil || started.Sub(at) > 60*time.Second || at.After(started.Add(5*time.Second)) {
		return zero, errors.New("allocation_snapshot_stale")
	}
	if r.Budget.MaxToolCalls < 6 {
		return zero, errors.New("agent_budget_exhausted")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.Budget.MaxDurationSeconds)*time.Second)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return zero, ctx.Err()
	}
	e := &execution{request: r}
	steps := []func() error{e.read, e.filter, e.rank, e.simulate, e.validate, e.submit}
	trace := []string{}
	for i, step := range steps {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if err := step(); err != nil {
			return zero, err
		}
		trace = append(trace, toolNames[i])
	}
	result := c.AllocationResponse{ProtocolVersion: Protocol, ResultSchemaVersion: Result, TaskID: r.TaskID, IdempotencyKey: r.IdempotencyKey, Pin: r.Pin, SnapshotHash: r.SnapshotHash, TerminalState: "DONE", Decisions: e.decisions, SafeTrace: c.AllocationTrace{ToolNames: trace, ToolCallCount: int64(len(trace)), DurationMS: s.now().Sub(started).Milliseconds()}}
	raw, err = json.Marshal(result)
	if err != nil || c.Validate("allocation.response", raw) != nil {
		return zero, errors.New("allocation_output_invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prior, exists := s.cache[r.IdempotencyKey]; exists && started.Sub(prior.at) < 5*time.Minute && prior.hash != inputHash {
		return zero, errors.New("idempotency_conflict")
	}
	for k, v := range s.cache {
		if started.Sub(v.at) >= 5*time.Minute {
			delete(s.cache, k)
		}
	}
	if len(s.cache) >= 256 {
		var oldest string
		var at time.Time
		for k, v := range s.cache {
			if oldest == "" || v.at.Before(at) {
				oldest, at = k, v.at
			}
		}
		delete(s.cache, oldest)
	}
	s.cache[r.IdempotencyKey] = entry{inputHash, cloneResponse(result), started}
	return result, nil
}
func cloneResponse(r c.AllocationResponse) c.AllocationResponse {
	raw, _ := json.Marshal(r)
	var v c.AllocationResponse
	_ = json.Unmarshal(raw, &v)
	return v
}

// PinEqual is used by HTTP clients/tests; pins are immutable values.
func PinEqual(a, b c.AllocationPin) bool { return reflect.DeepEqual(a, b) }
