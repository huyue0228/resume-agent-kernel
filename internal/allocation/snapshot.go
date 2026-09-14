// Package allocation computes plans from verified snapshots. It has no I/O tools.
package allocation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	c "resume-agent-kernel/internal/contract"
	"sort"
	"time"
)

func CanonicalSnapshot(s c.AllocationSnapshot) ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	s = c.AllocationSnapshot{}
	if err = json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	sort.Slice(s.Members, func(i, j int) bool { return s.Members[i].MemberID < s.Members[j].MemberID })
	sort.Slice(s.Demands, func(i, j int) bool { return s.Demands[i].DemandID < s.Demands[j].DemandID })
	for i := range s.Members {
		m := &s.Members[i]
		sort.Slice(m.Tags, func(i, j int) bool { return m.Tags[i].Code < m.Tags[j].Code })
		sort.Slice(m.CountedDemandIDs, func(i, j int) bool { return m.CountedDemandIDs[i] < m.CountedDemandIDs[j] })
		sort.Slice(m.AllowedDemandIDs, func(i, j int) bool { return m.AllowedDemandIDs[i] < m.AllowedDemandIDs[j] })
	}
	for i := range s.Demands {
		sort.Strings(s.Demands[i].RequiredTags)
		sort.Strings(s.Demands[i].PreferredTags)
	}
	raw, err = json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var v any
	if err = json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
func SnapshotHash(s c.AllocationSnapshot) (string, error) {
	raw, err := CanonicalSnapshot(s)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), err
}

func validateSnapshot(s c.AllocationSnapshot) error {
	invalid := errors.New("allocation_snapshot_invalid")
	demands := map[int64]bool{}
	members := map[int64]bool{}
	candidates := map[string]bool{}
	if _, err := time.Parse(time.RFC3339, s.SnapshotAt); err != nil {
		return invalid
	}
	if s.NextSequence < 1 {
		return invalid
	}
	for _, d := range s.Demands {
		if d.DemandID <= 0 || demands[d.DemandID] || d.LastAllocationSequence >= s.NextSequence {
			return invalid
		}
		demands[d.DemandID] = true
		for _, tags := range [][]string{d.RequiredTags, d.PreferredTags} {
			seen := map[string]bool{}
			for _, tag := range tags {
				if seen[tag] {
					return invalid
				}
				seen[tag] = true
			}
		}
	}
	for _, m := range s.Members {
		if m.TagsHash != TagHash(m.Tags) || m.MemberID <= 0 || members[m.MemberID] || candidates[m.CandidateRef] || !m.Admitted {
			return invalid
		}
		members[m.MemberID] = true
		candidates[m.CandidateRef] = true
		if _, err := time.Parse(time.RFC3339, m.CreatedAt); err != nil {
			return invalid
		}
		seen := map[int64]bool{}
		for _, id := range m.AllowedDemandIDs {
			if !demands[id] || seen[id] {
				return invalid
			}
			seen[id] = true
		}
		seen = map[int64]bool{}
		for _, id := range m.CountedDemandIDs {
			if !demands[id] || seen[id] {
				return invalid
			}
			seen[id] = true
		}
		tags := map[string]bool{}
		refs := map[string]bool{}
		for _, t := range m.Tags {
			if tags[t.Code] || refs[t.AssertionRef] || !t.Verified {
				return invalid
			}
			tags[t.Code] = true
			refs[t.AssertionRef] = true
		}
	}
	return nil
}

type option struct {
	demand     c.AllocationDemand
	hits, refs []string
	order      []int64
}

func eligible(m c.AllocationMember, demands map[int64]c.AllocationDemand) ([]option, string) {
	reason := "no_active_mapping"
	if len(m.AllowedDemandIDs) > 0 {
		reason = "no_receiving_demand"
	}
	tags := map[string]c.AllocationTag{}
	for _, t := range m.Tags {
		if t.Verified && t.Status == "supported" && (t.Source == "manual" || t.ConfidenceBPS >= 8000) {
			tags[t.Code] = t
		}
	}
	result := []option{}
	for _, id := range m.AllowedDemandIDs {
		d := demands[id]
		if d.ReceptionState != "receiving" {
			continue
		}
		reason = "required_tags_unavailable"
		ok := true
		hit := map[string]bool{}
		var score int64
		for _, t := range d.RequiredTags {
			if _, exists := tags[t]; !exists {
				ok = false
			}
			if _, exists := tags[t]; exists {
				hit[t] = true
			}
		}
		if !ok {
			continue
		}
		for _, t := range d.PreferredTags {
			if _, exists := tags[t]; exists {
				score++
				hit[t] = true
			}
		}
		if len(hit) == 0 {
			continue
		}
		keys := []string{}
		for k := range hit {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		refs := []string{}
		for _, k := range keys {
			refs = append(refs, tags[k].AssertionRef)
		}
		result = append(result, option{d, keys, refs, []int64{-score, d.Priority, d.RecentSupplyCount, d.LastAllocationSequence, d.DemandID}})
	}
	return result, reason
}
func less(a, b []int64) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func TagHash(tags []c.AllocationTag) string {
	copyTags := append([]c.AllocationTag{}, tags...)
	sort.Slice(copyTags, func(i, j int) bool { return copyTags[i].Code < copyTags[j].Code })
	raw, _ := json.Marshal(copyTags)
	var value any
	_ = json.Unmarshal(raw, &value)
	raw, _ = json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
