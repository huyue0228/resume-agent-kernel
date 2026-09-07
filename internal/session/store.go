package session

import (
	"context"
	"errors"
	p "resume-agent-kernel/internal/protocol"
	"sync"
	"time"
)

type entry struct {
	hash    string
	done    chan struct{}
	result  p.TaskResultV1
	err     error
	created time.Time
}
type Store struct {
	mu      sync.Mutex
	entries map[string]*entry
}

func (s *Store) Execute(ctx context.Context, key, hash string, run func() (p.TaskResultV1, error)) (p.TaskResultV1, error) {
	s.mu.Lock()
	if s.entries == nil {
		s.entries = map[string]*entry{}
	}
	if e, ok := s.entries[key]; ok {
		s.mu.Unlock()
		if e.hash != hash {
			return p.TaskResultV1{}, errors.New("idempotency payload conflict")
		}
		select {
		case <-ctx.Done():
			return p.TaskResultV1{}, ctx.Err()
		case <-e.done:
			return e.result, e.err
		}
	}
	for key, e := range s.entries {
		if time.Since(e.created) > time.Hour {
			select {
			case <-e.done:
				delete(s.entries, key)
			default:
			}
		}
	}
	if len(s.entries) >= 1000 {
		s.mu.Unlock()
		return p.TaskResultV1{}, errors.New("session capacity exhausted")
	}
	e := &entry{hash: hash, done: make(chan struct{}), created: time.Now()}
	s.entries[key] = e
	s.mu.Unlock()
	result, err := func() (result p.TaskResultV1, err error) {
		defer func() {
			if recover() != nil {
				err = errors.New("session execution failed")
			}
		}()
		return run()
	}()
	s.mu.Lock()
	e.result, e.err = result, err
	close(e.done)
	s.mu.Unlock()
	return result, err
}
