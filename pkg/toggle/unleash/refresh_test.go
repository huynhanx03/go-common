package unleash_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/unleash"
)

type sourceResult struct {
	snapshot toggle.Snapshot
	err      error
}

type queuedSource struct {
	mu      sync.Mutex
	current sourceResult
	queued  []sourceResult
}

func (s *queuedSource) Load(context.Context, toggle.Scope) (toggle.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queued) > 0 {
		result := s.queued[0]
		s.queued = s.queued[1:]
		return result.snapshot, result.err
	}
	return s.current.snapshot, s.current.err
}

func (s *queuedSource) push(snapshot toggle.Snapshot, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queued = append(s.queued, sourceResult{snapshot: snapshot, err: err})
}

func (s *queuedSource) publish(snapshot toggle.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = sourceResult{snapshot: snapshot}
}

func TestClientRefreshRejectsInvalidCandidates(t *testing.T) {
	initial := initialSnapshot()
	source := &queuedSource{current: sourceResult{snapshot: initial}}
	errorsSeen := make(chan error, 32)
	client, err := unleash.New(context.Background(), unleash.Config{
		Scope: testScope, AppName: "refresh-test", Source: source,
		RefreshInterval: 15 * time.Millisecond, SourceTimeout: time.Second,
		OnError: func(err error) { errorsSeen <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := client.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	duplicate := initialSnapshot()
	duplicate.Revision = 2
	duplicate.Flags[0].Enabled = false
	duplicate.Flags = append(duplicate.Flags, duplicate.Flags[0])

	oversized := toggle.Snapshot{Scope: testScope, Revision: 2, Flags: make([]toggle.Flag, 10001)}
	for i := range oversized.Flags {
		oversized.Flags[i] = toggle.Flag{Key: fmt.Sprintf("flag-%05d", i)}
	}

	candidates := []struct {
		name string
		item sourceResult
		want error
	}{
		{name: "source error", item: sourceResult{err: errors.New("private source detail")}, want: toggle.ErrSource},
		{name: "wrong scope", item: sourceResult{snapshot: toggle.Snapshot{Scope: toggle.Scope{Namespace: "other", Environment: "test"}, Revision: 2}}, want: toggle.ErrInvalidDefinition},
		{name: "duplicate keys", item: sourceResult{snapshot: duplicate}, want: toggle.ErrInvalidDefinition},
		{name: "negative revision", item: sourceResult{snapshot: toggle.Snapshot{Scope: testScope, Revision: -1}}, want: toggle.ErrInvalidDefinition},
		{name: "decreased revision", item: sourceResult{snapshot: toggle.Snapshot{Scope: testScope, Revision: 0}}, want: toggle.ErrStaleRevision},
		{name: "equal revision changed content", item: sourceResult{snapshot: func() toggle.Snapshot { v := initialSnapshot(); v.Flags[0].Enabled = false; return v }()}, want: toggle.ErrStaleRevision},
		{name: "oversized snapshot", item: sourceResult{snapshot: oversized}, want: toggle.ErrLimit},
	}
	for _, candidate := range candidates {
		t.Run(candidate.name, func(t *testing.T) {
			source.push(candidate.item.snapshot, candidate.item.err)
			observed := receiveSourceError(t, errorsSeen)
			if !errors.Is(observed, candidate.want) {
				t.Fatalf("OnError=%v, want errors.Is(_, %v)", observed, candidate.want)
			}
			checkEvaluation(t, client, "checkout", toggle.EvaluationContext{UserID: "u1"}, true)
		})
	}

	canonical := initialSnapshot()
	canonical.Flags[0], canonical.Flags[1] = canonical.Flags[1], canonical.Flags[0]
	for i := range canonical.Flags {
		if canonical.Flags[i].Key == "legacy" {
			canonical.Flags[i].Rules = []toggle.Rule{}
		}
	}
	before := len(errorsSeen)
	source.push(canonical, nil)
	time.Sleep(100 * time.Millisecond)
	if after := len(errorsSeen); after != before {
		t.Fatalf("normalized-equivalent snapshot produced %d errors", after-before)
	}
	checkEvaluation(t, client, "checkout", toggle.EvaluationContext{UserID: "u1"}, true)

	updated := initialSnapshot()
	updated.Revision = 2
	updated.Flags[0].Enabled = false
	source.publish(updated)
	awaitDisabled(t, client)

	empty := toggle.Snapshot{Scope: testScope, Revision: 3, Flags: []toggle.Flag{}}
	source.publish(empty)
	waitEnabledWithFallback(t, client, "checkout", "u1")
}

func receiveSourceError(t *testing.T, errorsSeen <-chan error) error {
	t.Helper()
	select {
	case err := <-errorsSeen:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("refresh error was not reported")
		return nil
	}
}

func waitEnabledWithFallback(t *testing.T, client toggle.Client, key, user string) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		enabled, err := client.IsEnabled(context.Background(), key, toggle.EvaluationContext{UserID: user}, true)
		if err != nil {
			t.Fatal(err)
		}
		if enabled {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("empty source did not remove the flag")
		case <-ticker.C:
		}
	}
}
