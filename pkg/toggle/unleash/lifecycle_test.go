package unleash_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/unleash"
)

func TestClientConcurrentCloseAndCancellation(t *testing.T) {
	enteredRefresh := make(chan struct{})
	releaseSource := make(chan struct{})
	var calls atomic.Int32
	var enteredOnce sync.Once
	source := sourceFunc(func(ctx context.Context, _ toggle.Scope) (toggle.Snapshot, error) {
		if calls.Add(1) == 1 {
			return initialSnapshot(), nil
		}
		enteredOnce.Do(func() { close(enteredRefresh) })
		<-releaseSource // Deliberately violates the Source context contract for this close-timeout case.
		candidate := initialSnapshot()
		candidate.Revision = 2
		candidate.Flags[0].Enabled = false
		return candidate, nil
	})
	client, err := unleash.New(context.Background(), unleash.Config{
		Scope: testScope, AppName: "close-timeout-test", Source: source,
		RefreshInterval: 10 * time.Millisecond, SourceTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	var released atomic.Bool
	release := func() {
		if released.CompareAndSwap(false, true) {
			close(releaseSource)
		}
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Close(ctx); err != nil {
			t.Errorf("cleanup close: %v", err)
		}
	}()
	defer release()
	select {
	case <-enteredRefresh:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not enter the source")
	}

	laterClose := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		laterClose <- client.Close(ctx)
	}()
	shortCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	firstErr := client.Close(shortCtx)
	cancel()
	if !errors.Is(firstErr, context.DeadlineExceeded) {
		t.Fatalf("first Close error = %v, want deadline exceeded", firstErr)
	}
	if enabled, err := client.IsEnabled(context.Background(), "checkout", toggle.EvaluationContext{}, true); !enabled || !errors.Is(err, toggle.ErrClosed) {
		t.Fatalf("evaluation during cleanup = %v, %v", enabled, err)
	}
	release()
	select {
	case err := <-laterClose:
		if err != nil {
			t.Fatalf("later Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("later Close did not observe completed cleanup")
	}
}

func TestClientWellBehavedSourceStopsOnClose(t *testing.T) {
	enteredRefresh := make(chan struct{})
	var calls atomic.Int32
	var enteredOnce sync.Once
	client := newClient(t, sourceFunc(func(ctx context.Context, _ toggle.Scope) (toggle.Snapshot, error) {
		if calls.Add(1) == 1 {
			return initialSnapshot(), nil
		}
		enteredOnce.Do(func() { close(enteredRefresh) })
		<-ctx.Done()
		return toggle.Snapshot{}, ctx.Err()
	}), testScope, 10*time.Millisecond)
	select {
	case <-enteredRefresh:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not enter the source")
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("context-aware source delayed Close for %s", elapsed)
	}
}

func TestClientReportsRepeatedSourceErrors(t *testing.T) {
	var calls atomic.Int32
	reported := make(chan error, 32)
	client, err := unleash.New(context.Background(), unleash.Config{
		Scope: testScope, AppName: "source-errors-test", RefreshInterval: 10 * time.Millisecond, SourceTimeout: time.Second,
		Source: sourceFunc(func(context.Context, toggle.Scope) (toggle.Snapshot, error) {
			if calls.Add(1) == 1 {
				return initialSnapshot(), nil
			}
			return toggle.Snapshot{}, errors.New("private source details")
		}),
		OnError: func(err error) { reported <- err },
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
	for i := 0; i < 8; i++ {
		select {
		case err := <-reported:
			if !errors.Is(err, toggle.ErrSource) || strings.Contains(err.Error(), "private source details") {
				t.Fatalf("source error is not safely categorized: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("SDK stopped draining refresh errors after %d callbacks", i)
		}
	}
}

func TestClientRejectsInvalidEvaluationInputs(t *testing.T) {
	client := newEvaluationClient(t, []toggle.Flag{{Key: "checkout", Enabled: true}}, nil)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tooManyProperties := make(map[string]string, 101)
	for i := 0; i < 101; i++ {
		tooManyProperties[fmt.Sprintf("property-%03d", i)] = "value"
	}
	cases := []struct {
		name       string
		ctx        context.Context
		key        string
		evaluation toggle.EvaluationContext
		want       error
	}{
		{name: "nil context", ctx: nil, key: "checkout", want: toggle.ErrInvalidContext},
		{name: "canceled context", ctx: canceled, key: "checkout", want: context.Canceled},
		{name: "invalid key", ctx: context.Background(), key: "bad key", want: toggle.ErrInvalidContext},
		{name: "reserved property", ctx: context.Background(), key: "checkout", evaluation: toggle.EvaluationContext{Properties: map[string]string{"appName": "spoofed"}}, want: toggle.ErrInvalidContext},
		{name: "oversized property", ctx: context.Background(), key: "checkout", evaluation: toggle.EvaluationContext{Properties: map[string]string{"tenant": strings.Repeat("x", 4097)}}, want: toggle.ErrLimit},
		{name: "too many properties", ctx: context.Background(), key: "checkout", evaluation: toggle.EvaluationContext{Properties: tooManyProperties}, want: toggle.ErrLimit},
		{name: "oversized identity", ctx: context.Background(), key: "checkout", evaluation: toggle.EvaluationContext{UserID: strings.Repeat("x", 4097)}, want: toggle.ErrLimit},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			enabled, err := client.IsEnabled(test.ctx, test.key, test.evaluation, true)
			if !enabled || !errors.Is(err, test.want) {
				t.Fatalf("IsEnabled fallback=%v err=%v, want fallback=true and errors.Is(_, %v)", enabled, err, test.want)
			}
			variant, err := client.GetVariant(test.ctx, test.key, test.evaluation)
			if !errors.Is(err, test.want) || variant.Enabled || variant.FeatureEnabled || variant.Name != "disabled" {
				t.Fatalf("GetVariant invalid result=%+v err=%v, want disabled and errors.Is(_, %v)", variant, err, test.want)
			}
		})
	}
}

func TestClientOwnsAdmittedSourceData(t *testing.T) {
	snapshot := initialSnapshot()
	source := &controlledSource{snapshot: snapshot}
	client := newClient(t, source, testScope, time.Hour)
	snapshot.Flags[0].Enabled = false
	snapshot.Flags[0].Rules[0].Variants[0].Payload.Value = "mutated-source-payload"
	checkEvaluation(t, client, "checkout", toggle.EvaluationContext{UserID: "u1"}, true)
	variant, err := client.GetVariant(context.Background(), "checkout", toggle.EvaluationContext{UserID: "u1"})
	if err != nil || variant.Payload == nil || variant.Payload.Value != `{"version":2}` {
		t.Fatalf("source mutation escaped into client: %+v, %v", variant, err)
	}
}
