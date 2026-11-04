package i18n

import (
	"context"
	"sync"
	"testing"

	"github.com/huynhanx03/go-common/pkg/common/cache"
	"github.com/huynhanx03/go-common/pkg/common/cache/ristretto"
	jsoncodec "github.com/huynhanx03/go-common/pkg/encoding/json"
	"github.com/huynhanx03/go-common/pkg/logger"
)

type coverageStore struct {
	mu          sync.Mutex
	states      map[string]State
	readErr     error
	writeErr    error
	deleteErr   error
	domainsErr  error
	domainsSeed []string
}

func newCoverageStore() *coverageStore {
	return &coverageStore{states: make(map[string]State)}
}

func (s *coverageStore) Read(ctx context.Context, domain string) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return State{}, s.readErr
	}
	state, ok := s.states[domain]
	if !ok {
		return State{}, ErrNotFound
	}
	return cloneTestState(state)
}

func (s *coverageStore) CompareAndSwap(ctx context.Context, domain string, expected int64, next State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	current, ok := s.states[domain]
	if (!ok && expected != 0) || (ok && current.Draft.Revision != expected) {
		return ErrConflict
	}
	cloned, err := cloneTestState(next)
	if err != nil {
		return err
	}
	s.states[domain] = cloned
	return nil
}

func (s *coverageStore) Delete(ctx context.Context, domain string, expected int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	state, ok := s.states[domain]
	if !ok {
		return ErrNotFound
	}
	if state.Draft.Revision != expected {
		return ErrConflict
	}
	delete(s.states, domain)
	return nil
}

func (s *coverageStore) Domains(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.domainsErr != nil {
		return nil, s.domainsErr
	}
	if s.domainsSeed != nil {
		return append([]string(nil), s.domainsSeed...), nil
	}
	domains := make([]string, 0, len(s.states))
	for domain := range s.states {
		domains = append(domains, domain)
	}
	return domains, nil
}

func cloneTestState(state State) (State, error) {
	encoded, err := jsoncodec.Marshal(state)
	if err != nil {
		return State{}, err
	}
	var cloned State
	err = jsoncodec.Unmarshal(encoded, &cloned)
	return cloned, err
}

func newCoverageCache(t testing.TB) *cache.Cache {
	t.Helper()
	l1, err := ristretto.New[string, any](ristretto.WithNumCounters(10000), ristretto.WithMaxCost(8<<20))
	if err != nil {
		t.Fatal(err)
	}
	log, err := logger.NewLogger(logger.LoggerConfig{Level: "error"})
	if err != nil {
		l1.Close()
		t.Fatal(err)
	}
	shared, err := cache.New(cache.Config{L1: l1, Logger: log})
	if err != nil {
		l1.Close()
		t.Fatal(err)
	}
	t.Cleanup(shared.Close)
	return shared
}

func newCoverageManager(t testing.TB, store *coverageStore) *Manager {
	t.Helper()
	manager, err := NewManager(store, newCoverageCache(t))
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

var _ Store = (*coverageStore)(nil)
