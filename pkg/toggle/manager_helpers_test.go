package toggle_test

import (
	"context"
	"sync"

	"github.com/huynhanx03/go-common/pkg/toggle"
)

// recordingStore deliberately returns and retains borrowed values. Manager
// scenarios use it to exercise the public ownership boundary, not SQL behavior.
type recordingStore struct {
	mu sync.Mutex

	snapshot toggle.Snapshot
	loadErr  error
	putErr   error
	delErr   error

	loads          int
	puts           int
	deletes        int
	lastScope      toggle.Scope
	lastFlag       toggle.Flag
	lastKey        string
	putExpected    int64
	deleteExpected int64
	putRevision    int64
	delRevision    int64
}

func (s *recordingStore) Load(_ context.Context, _ toggle.Scope) (toggle.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	return s.snapshot, s.loadErr
}

func (s *recordingStore) Put(_ context.Context, scope toggle.Scope, flag toggle.Flag, expected int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	s.lastScope, s.lastFlag, s.putExpected = scope, flag, expected
	return s.putRevision, s.putErr
}

func (s *recordingStore) Delete(_ context.Context, scope toggle.Scope, key string, expected int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	s.lastScope, s.lastKey, s.deleteExpected = scope, key, expected
	return s.delRevision, s.delErr
}

var managerScope = toggle.Scope{Namespace: "billing", Environment: "test"}

func validManagerFlag(key string) toggle.Flag {
	return toggle.Flag{Key: key, Enabled: true, Rules: []toggle.Rule{{Rollout: 100}}}
}
