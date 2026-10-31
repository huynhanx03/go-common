package unleash_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/toggle"
	"github.com/huynhanx03/go-common/pkg/toggle/unleash"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Catches bypassing bootstrap/SDK mapping, treating disabled as missing, leaking variants,
// or fetching from Source/network on the evaluation path.
func TestClientBootstrapAndLocalEvaluation(t *testing.T) {
	var network atomic.Int32
	old := http.DefaultTransport
	http.DefaultTransport = transportFunc(func(*http.Request) (*http.Response, error) {
		network.Add(1)
		return nil, errors.New("unexpected network")
	})
	defer func() { http.DefaultTransport = old }()
	source := &controlledSource{snapshot: initialSnapshot()}
	client := newClient(t, source, testScope, time.Hour)
	if source.calls() != 1 {
		t.Fatalf("initial loads=%d", source.calls())
	}
	ctx := context.Background()
	enabled, err := client.IsEnabled(ctx, "checkout", toggle.EvaluationContext{UserID: "u1"}, false)
	if err != nil || !enabled {
		t.Fatalf("enabled=%v err=%v", enabled, err)
	}
	legacy, err := client.IsEnabled(ctx, "legacy", toggle.EvaluationContext{}, true)
	if err != nil || legacy {
		t.Fatalf("disabled flag used fallback: %v %v", legacy, err)
	}
	variant, err := client.GetVariant(ctx, "checkout", toggle.EvaluationContext{UserID: "u1"})
	if err != nil || !variant.Enabled || !variant.FeatureEnabled || variant.Name != "v2" {
		t.Fatalf("variant=%+v err=%v", variant, err)
	}
	if variant.Payload == nil || variant.Payload.Type != "json" || variant.Payload.Value != `{"version":2}` {
		t.Fatalf("payload=%+v", variant.Payload)
	}
	variant.Payload.Value = "changed"
	variant, err = client.GetVariant(ctx, "checkout", toggle.EvaluationContext{UserID: "u1"})
	if err != nil || variant.Payload == nil || variant.Payload.Value != `{"version":2}` {
		t.Fatalf("payload was not copied: %+v %v", variant, err)
	}
	unknown, err := client.IsEnabled(ctx, "unknown", toggle.EvaluationContext{}, true)
	if err != nil || !unknown {
		t.Fatalf("unknown=%v err=%v", unknown, err)
	}
	for _, key := range []string{"unknown", "legacy", "plain"} {
		v, err := client.GetVariant(ctx, key, toggle.EvaluationContext{})
		if err != nil || v.Enabled || v.FeatureEnabled != (key == "plain") {
			t.Fatalf("%s variant=%+v err=%v", key, v, err)
		}
	}
	for i := 0; i < 1000; i++ {
		enabled, err := client.IsEnabled(ctx, "checkout", toggle.EvaluationContext{UserID: "u1"}, false)
		if err != nil || !enabled {
			t.Fatalf("check %d: %v %v", i, enabled, err)
		}
	}
	if source.calls() != 1 {
		t.Fatalf("evaluation loaded source %d times", source.calls())
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if network.Load() != 0 {
		t.Fatalf("external calls=%d", network.Load())
	}
	_, err = client.IsEnabled(ctx, "checkout", toggle.EvaluationContext{}, true)
	if !errors.Is(err, toggle.ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
}

// Catches a second refresh scheduler or synchronous source work during evaluation.
func TestClientRefreshUsesSource(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	releaseRefresh := sync.OnceFunc(func() { close(release) })
	defer releaseRefresh()
	var calls atomic.Int32
	client := newClient(t, sourceFunc(func(ctx context.Context, scope toggle.Scope) (toggle.Snapshot, error) {
		n := calls.Add(1)
		snapshot := initialSnapshot()
		if n == 1 {
			return snapshot, nil
		}
		if n == 2 {
			close(entered)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return toggle.Snapshot{}, ctx.Err()
		}
		snapshot.Revision = 2
		snapshot.Flags[0].Enabled = false
		return snapshot, nil
	}), testScope, 20*time.Millisecond)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not load source")
	}
	finished := make(chan error, 1)
	go func() {
		for i := 0; i < 1000; i++ {
			enabled, err := client.IsEnabled(context.Background(), "checkout", toggle.EvaluationContext{UserID: "u1"}, false)
			if err != nil {
				finished <- err
				return
			}
			if !enabled {
				finished <- errors.New("flag changed before gated refresh")
				return
			}
		}
		finished <- nil
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("evaluation waited for refresh")
	}
	if calls.Load() != 2 {
		t.Fatalf("loads while refresh blocked=%d", calls.Load())
	}
	releaseRefresh()
	awaitDisabled(t, client)
}

func TestClientCanceledBootstrap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		c, err := unleash.New(ctx, unleash.Config{Scope: testScope, AppName: "billing-test", Source: sourceFunc(func(ctx context.Context, _ toggle.Scope) (toggle.Snapshot, error) {
			close(entered)
			<-ctx.Done()
			return toggle.Snapshot{}, ctx.Err()
		})})
		if c != nil {
			_ = c.Close(context.Background())
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("bootstrap did not load source")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled bootstrap: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bootstrap ignored cancellation")
	}
}
