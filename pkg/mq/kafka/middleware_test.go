package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

func TestChainWrapsLegacyHandlersInDeclarationOrder(t *testing.T) {
	var calls []string
	handler := Chain(
		func(context.Context, []byte, []byte) error {
			calls = append(calls, "handler")
			return nil
		},
		func(next Handler) Handler {
			return func(ctx context.Context, key, value []byte) error {
				calls = append(calls, "outer-before")
				err := next(ctx, key, value)
				calls = append(calls, "outer-after")
				return err
			}
		},
		func(next Handler) Handler {
			return func(ctx context.Context, key, value []byte) error {
				calls = append(calls, "inner-before")
				err := next(ctx, key, value)
				calls = append(calls, "inner-after")
				return err
			}
		},
	)
	if err := handler(context.Background(), []byte("key"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	want := []string{"outer-before", "inner-before", "handler", "inner-after", "outer-after"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("middleware calls = %v, want %v", calls, want)
	}

	failure := errors.New("handler failed")
	plain := func(context.Context, []byte, []byte) error { return failure }
	if err := Chain(plain)(context.Background(), nil, nil); !errors.Is(err, failure) {
		t.Fatalf("handler error changed without middleware: %v", err)
	}
}

func TestRecoveryTurnsLegacyHandlerPanicsIntoErrors(t *testing.T) {
	wrapped := Recovery(func(context.Context, []byte, []byte) error { panic("handler panic") })
	if err := wrapped(context.Background(), nil, nil); !errors.Is(err, consumer.ErrHandlerPanic) || !strings.Contains(err.Error(), "handler panic") {
		t.Fatalf("panic error = %v", err)
	}
	failure := errors.New("handler failed")
	wrapped = Recovery(func(context.Context, []byte, []byte) error { return failure })
	if err := wrapped(context.Background(), nil, nil); !errors.Is(err, failure) {
		t.Fatalf("ordinary handler error changed: %v", err)
	}
	wrapped = Recovery(func(context.Context, []byte, []byte) error { return nil })
	if err := wrapped(context.Background(), nil, nil); err != nil {
		t.Fatalf("successful handler returned error: %v", err)
	}
}
