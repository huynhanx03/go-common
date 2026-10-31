package unleash_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/unleash"
)

type controlledSource struct {
	mu       sync.Mutex
	snapshot toggle.Snapshot
	err      error
	loads    int
}

func (s *controlledSource) Load(ctx context.Context, scope toggle.Scope) (toggle.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	return s.snapshot, s.err
}
func (s *controlledSource) replace(snapshot toggle.Snapshot, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot, s.err = snapshot, err
}
func (s *controlledSource) calls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.loads }

type sourceFunc func(context.Context, toggle.Scope) (toggle.Snapshot, error)

func (f sourceFunc) Load(ctx context.Context, scope toggle.Scope) (toggle.Snapshot, error) {
	return f(ctx, scope)
}
func newClient(t *testing.T, source toggle.Source, scope toggle.Scope, interval time.Duration) toggle.Client {
	t.Helper()
	c, err := unleash.New(context.Background(), unleash.Config{Scope: scope, AppName: "billing-test", Source: source, RefreshInterval: interval, SourceTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return c
}

var testScope = toggle.Scope{Namespace: "billing", Environment: "test"}

func initialSnapshot() toggle.Snapshot {
	return toggle.Snapshot{Scope: testScope, Revision: 1, Flags: []toggle.Flag{{Key: "checkout", Enabled: true, Rules: []toggle.Rule{{Rollout: 100, Variants: []toggle.Variant{{Name: "v2", Weight: 1000, Payload: &toggle.Payload{Type: "json", Value: `{"version":2}`}}}}}}, {Key: "legacy", Enabled: false}, {Key: "plain", Enabled: true}}}
}
func awaitDisabled(t *testing.T, c toggle.Client) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		enabled, err := c.IsEnabled(context.Background(), "checkout", toggle.EvaluationContext{UserID: "u1"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if !enabled {
			return
		}
		select {
		case <-deadline:
			t.Fatal("refresh did not disable checkout")
		case <-ticker.C:
		}
	}
}
