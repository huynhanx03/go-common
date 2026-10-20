package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"
)

type blockingEngine struct {
	CacheEngine
	started chan struct{}
	release chan struct{}
}

func (engine *blockingEngine) Get(
	ctx context.Context,
	_ string,
) ([]byte, bool, error) {
	select {
	case engine.started <- struct{}{}:
	default:
	}
	select {
	case <-engine.release:
		return nil, false, ErrKeyNotFound
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
}

func TestFetchRemoteWaiterCancellationDoesNotSpawnDuplicateLoader(t *testing.T) {
	t.Parallel()

	engine := newFakeEngine()
	group := &singleflight.Group{}
	started := make(chan struct{})
	release := make(chan struct{})
	loader := func(context.Context) (string, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return "remote", nil
	}
	first := make(chan error, 1)
	go func() {
		_, err := FetchRemote(
			context.Background(),
			engine,
			group,
			"remote-key",
			time.Minute,
			loader,
		)
		first <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FetchRemote(
		ctx,
		engine,
		group,
		"remote-key",
		time.Minute,
		loader,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled FetchRemote error = %v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first FetchRemote: %v", err)
	}
}

func TestRemoteHelpersRejectCanceledContextBeforeEngineCall(t *testing.T) {
	t.Parallel()

	engine := &blockingEngine{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := GetRemote[string](ctx, engine, "key"); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("GetRemote error = %v", err)
	}
	select {
	case <-engine.started:
		t.Fatal("engine was called for an already canceled request")
	default:
	}
}
