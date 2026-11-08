package redis

import (
	"context"
	"errors"
	"reflect"
	"testing"

	redisV9 "github.com/redis/go-redis/v9"
)

func TestRunScriptPublicContract(t *testing.T) {
	ctx := context.Background()
	wantResult := int64(23)
	client := &scriptClientStub{result: wantResult}
	engine := &RedisEngine{client: client, namespace: "tenant-a"}
	script := redisV9.NewScript("return 23")
	keys := []string{"counter:{user-4}", "marker:{user-4}:op-2"}
	args := []any{int64(7), []byte("raw")}
	wantKeys := append([]string(nil), keys...)
	wantArgs := append([]any(nil), args...)

	result, err := engine.RunScript(ctx, script, keys, args...)
	if err != nil {
		t.Fatalf("RunScript() error = %v", err)
	}
	if result != wantResult {
		t.Fatalf("RunScript() result = %#v; want %#v", result, wantResult)
	}
	if client.calls != 1 {
		t.Fatalf("script calls = %d; want 1", client.calls)
	}
	if client.ctx != ctx {
		t.Fatal("RunScript() did not pass through the caller context")
	}
	if want := []string{"tenant-a:counter:{user-4}", "tenant-a:marker:{user-4}:op-2"}; !reflect.DeepEqual(client.keys, want) {
		t.Fatalf("script keys = %#v; want %#v", client.keys, want)
	}
	if !reflect.DeepEqual(client.args, wantArgs) {
		t.Fatalf("script args = %#v; want %#v", client.args, wantArgs)
	}
	if !reflect.DeepEqual(keys, wantKeys) || !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("RunScript() modified caller inputs: keys=%#v args=%#v", keys, args)
	}

	t.Run("rejects nil script before accessing the client", func(t *testing.T) {
		engine := &RedisEngine{}
		if _, err := engine.RunScript(ctx, nil, nil); !errors.Is(err, ErrInvalidScript) {
			t.Fatalf("RunScript(nil script) error = %v; want ErrInvalidScript", err)
		}
	})

	t.Run("preserves native errors", func(t *testing.T) {
		cause := errors.New("connection lost")
		nativeErr := &scriptReplyError{cause: cause}
		client := &scriptClientStub{err: nativeErr}
		engine := &RedisEngine{client: client}

		_, err := engine.RunScript(ctx, script, []string{"key"})
		var gotNative *scriptReplyError
		if !errors.Is(err, cause) || !errors.As(err, &gotNative) || gotNative != nativeErr {
			t.Fatalf("RunScript() error = %#v; want original native error wrapping %#v", err, cause)
		}
	})

	t.Run("lost response is returned without a wrapper retry", func(t *testing.T) {
		transportErr := errors.New("reply lost after Redis applied the script")
		client := &scriptClientStub{err: transportErr, applyBeforeError: true}
		engine := &RedisEngine{client: client}

		_, err := engine.RunScript(ctx, script, []string{"counter:{7}"}, "operation-9")
		if !errors.Is(err, transportErr) {
			t.Fatalf("RunScript() error = %v; want original transport error", err)
		}
		if !client.applied {
			t.Fatal("test script was not applied before its response was lost")
		}
		if client.calls != 1 {
			t.Fatalf("script calls after ambiguous response = %d; want 1", client.calls)
		}
	})
}

type scriptClientStub struct {
	redisV9.UniversalClient
	ctx              context.Context
	keys             []string
	args             []any
	result           any
	err              error
	calls            int
	applied          bool
	applyBeforeError bool
}

func (c *scriptClientStub) EvalSha(ctx context.Context, _ string, keys []string, args ...any) *redisV9.Cmd {
	c.calls++
	c.ctx = ctx
	c.keys = append([]string(nil), keys...)
	c.args = append([]any(nil), args...)
	if c.applyBeforeError {
		c.applied = true
	}
	return redisV9.NewCmdResult(c.result, c.err)
}

type scriptReplyError struct {
	cause error
}

func (e *scriptReplyError) Error() string { return "Redis script reply failed" }

func (e *scriptReplyError) Unwrap() error { return e.cause }
