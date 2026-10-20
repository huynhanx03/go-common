package cache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"
)

func TestGetOrLoadIsSingleflightAndWaitersCanCancel(t *testing.T) {
	t.Parallel()

	local := newFakeLocal()
	group := &singleflight.Group{}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	loader := func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return "loaded", nil
	}

	first := make(chan error, 1)
	go func() {
		value, err := GetOrLoad(
			context.Background(),
			local,
			group,
			"key",
			time.Minute,
			loader,
		)
		if value != "loaded" && err == nil {
			err = errors.New("unexpected value")
		}
		first <- err
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := GetOrLoad(
		ctx,
		local,
		group,
		"key",
		time.Minute,
		loader,
	); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled waiter error = %v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first loader: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d", calls.Load())
	}
}

func TestGetOrLoadRejectsInvalidContract(t *testing.T) {
	t.Parallel()

	local := newFakeLocal()
	group := &singleflight.Group{}
	loader := func(context.Context) (int, error) { return 1, nil }
	tests := []struct {
		cache  LocalCache[string, any]
		group  *singleflight.Group
		key    string
		ttl    time.Duration
		loader func(context.Context) (int, error)
	}{
		{group: group, key: "key", ttl: time.Minute, loader: loader},
		{cache: local, key: "key", ttl: time.Minute, loader: loader},
		{cache: local, group: group, ttl: time.Minute, loader: loader},
		{cache: local, group: group, key: "key", loader: loader},
		{cache: local, group: group, key: "key", ttl: time.Minute},
	}
	for index, test := range tests {
		if _, err := GetOrLoad(
			context.Background(),
			test.cache,
			test.group,
			test.key,
			test.ttl,
			test.loader,
		); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case[%d] error = %v", index, err)
		}
	}
}
