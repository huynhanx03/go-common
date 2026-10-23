package redis

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRedisBatchLimitsAndInvalidValues(t *testing.T) {
	engine := &RedisEngine{}
	ctx := context.Background()

	tooManyKeys := make([]string, maxBatchCommands+1)
	if err := engine.DeleteBulk(ctx, tooManyKeys); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("DeleteBulk over limit error = %v; want ErrBatchTooLarge", err)
	}
	tooManyValues := make(map[string]any, maxBatchCommands+1)
	for i := 0; i <= maxBatchCommands; i++ {
		tooManyValues[fmt.Sprint(i)] = i
	}
	if err := engine.BatchSet(ctx, tooManyValues, time.Minute); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("BatchSet over limit error = %v; want ErrBatchTooLarge", err)
	}
	if _, err := engine.SetNX(ctx, "key", make(chan int), time.Minute); err == nil {
		t.Fatal("SetNX with unencodable value should fail")
	}
}

func TestScopedKey(t *testing.T) {
	for _, test := range []struct {
		name      string
		namespace string
		key       string
		want      string
	}{
		{name: "with namespace", namespace: "judgify:prod", key: "profile", want: "judgify:prod:profile"},
		{name: "without namespace", key: "profile", want: "profile"},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := &RedisEngine{namespace: test.namespace}
			if got := engine.scopedKey(test.key); got != test.want {
				t.Fatalf("scopedKey(%q) = %q; want %q", test.key, got, test.want)
			}
		})
	}
}
