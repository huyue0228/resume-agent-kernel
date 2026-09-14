package allocation

import (
	"context"
	"encoding/json"
	"reflect"
	c "resume-agent-kernel/internal/contract"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) c.AllocationRequest {
	t.Helper()
	raw, err := c.Bundle.ReadFile("bundle/allocation.request.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var r c.AllocationRequest
	if err = json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}
func service() *Service {
	s := New("dev")
	s.now = func() time.Time { return time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC) }
	return s
}
func TestCanonicalCrossLanguage(t *testing.T) {
	raw, _ := c.Bundle.ReadFile("bundle/allocation.hash-vectors.json")
	var cases []struct {
		Snapshot          c.AllocationSnapshot
		Canonical, Sha256 string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, v := range cases {
		raw, err := CanonicalSnapshot(v.Snapshot)
		if err != nil || string(raw) != v.Canonical {
			t.Fatalf("canonical mismatch %v", err)
		}
		h, _ := SnapshotHash(v.Snapshot)
		if h != v.Sha256 {
			t.Fatal("hash mismatch")
		}
	}
}
func TestDesignCases(t *testing.T) {
	raw, _ := c.Bundle.ReadFile("bundle/allocation.cases.json")
	var cases []struct {
		ID       string
		Request  c.AllocationRequest
		Expected []struct {
			MemberID int64 `json:"member_id"`
			DemandID int64 `json:"demand_id"`
			Wait     string
		}
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			s := service()
			r, err := s.Execute(context.Background(), tc.Request)
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range tc.Expected {
				got := r.Decisions[i]
				if got.MemberID != want.MemberID || got.DemandID != want.DemandID || (want.Wait != "" && got.ReasonCode != want.Wait) {
					t.Fatalf("decision %d = %+v want %+v", i, got, want)
				}
			}
			// Transport order cannot change the result or hash.
			req := tc.Request
			for i, j := 0, len(req.Snapshot.Members)-1; i < j; i, j = i+1, j-1 {
				req.Snapshot.Members[i], req.Snapshot.Members[j] = req.Snapshot.Members[j], req.Snapshot.Members[i]
			}
			hash, _ := SnapshotHash(req.Snapshot)
			if hash != req.SnapshotHash {
				t.Fatal("unordered snapshot hash")
			}
			second, err := s.Execute(context.Background(), req)
			if err != nil || !reflect.DeepEqual(r.Decisions, second.Decisions) {
				t.Fatal("unstable replay", err)
			}
		})
	}
}
func TestRejectInvalidAndIsolateCache(t *testing.T) {
	r := fixture(t)
	s := service()
	first, err := s.Execute(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	first.Decisions[0].MatchedTags[0] = "tampered"
	again, err := s.Execute(context.Background(), r)
	if err != nil || again.Decisions[0].MatchedTags[0] != "backend" || !again.SafeTrace.Reused {
		t.Fatal("mutable cache")
	}
	r.Snapshot.Demands[0].Priority++
	r.SnapshotHash, _ = SnapshotHash(r.Snapshot)
	if _, err = s.Execute(context.Background(), r); err == nil || err.Error() != "idempotency_conflict" {
		t.Fatal("expected conflict", err)
	}
	for _, mutate := range []func(*c.AllocationRequest){func(r *c.AllocationRequest) { r.SnapshotHash = strings.Repeat("0", 64) }, func(r *c.AllocationRequest) { r.Pin.KernelBuild = "old" }, func(r *c.AllocationRequest) { r.Budget.MaxToolCalls = 5 }, func(r *c.AllocationRequest) {
		r.Snapshot.Members = append(r.Snapshot.Members, r.Snapshot.Members[0])
		r.SnapshotHash, _ = SnapshotHash(r.Snapshot)
	}, func(r *c.AllocationRequest) {
		r.Snapshot.SnapshotAt = "2026-09-13T00:00:00Z"
		r.SnapshotHash, _ = SnapshotHash(r.Snapshot)
	}} {
		r = fixture(t)
		mutate(&r)
		if _, err := service().Execute(context.Background(), r); err == nil {
			t.Fatal("invalid accepted")
		}
	}
}
func TestConcurrentReplayAndCancellation(t *testing.T) {
	s := service()
	r := fixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.Execute(context.Background(), r)
			if err != nil || len(res.Decisions) != 1 {
				t.Errorf("concurrent replay: %v", err)
			}
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service().Execute(ctx, r); err == nil {
		t.Fatal("cancelled executed")
	}
	for _, name := range s.Capabilities().ToolNames {
		if !strings.HasPrefix(name, "allocation.") {
			t.Fatal("tool leak")
		}
	}
}
