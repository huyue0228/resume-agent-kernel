package session

import (
	"context"
	p "resume-agent-kernel/internal/protocol"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentIdempotencyAndConflictingPayload(t *testing.T) {
	s := &Store{}
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Execute(context.Background(), "key", "hash", func() (p.TaskResultV1, error) { calls.Add(1); return p.TaskResultV1{TaskID: "same"}, nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	if _, err := s.Execute(context.Background(), "key", "other", nil); err == nil {
		t.Fatal("conflicting payload accepted")
	}
}
