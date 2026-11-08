package consumer_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/huynhanx03/go-common/pkg/logger"
	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

func TestMessageIdentityPrefersValidStableHeaders(t *testing.T) {
	for _, test := range []struct {
		name    string
		headers []consumer.Header
		want    string
	}{
		{
			name: "canonical identity wins over legacy outbox identity",
			headers: []consumer.Header{
				{Key: consumer.OutboxMessageIDHeader, Value: []byte("legacy-id")},
				{Key: consumer.MessageIDHeader, Value: []byte("stable-id")},
			},
			want: "stable-id",
		},
		{
			name: "first valid legacy identity is used when canonical identity is absent",
			headers: []consumer.Header{
				{Key: consumer.OutboxMessageIDHeader, Value: []byte("bad\x00id")},
				{Key: consumer.OutboxMessageIDHeader, Value: []byte("legacy-id")},
				{Key: consumer.OutboxMessageIDHeader, Value: []byte("later-id")},
			},
			want: "legacy-id",
		},
		{
			name: "invalid canonical identity does not hide valid legacy identity",
			headers: []consumer.Header{
				{Key: consumer.MessageIDHeader, Value: []byte(" bad-id")},
				{Key: consumer.OutboxMessageIDHeader, Value: []byte("legacy-id")},
			},
			want: "legacy-id",
		},
		{name: "unrelated headers do not become an identity", headers: []consumer.Header{{Key: "traceparent", Value: []byte("trace")}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := consumer.IDFromHeaders(test.headers); got != test.want {
				t.Fatalf("IDFromHeaders() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMessageIdentityValidationRejectsUnsafeDeduplicationKeys(t *testing.T) {
	for _, test := range []struct {
		id   string
		want bool
	}{
		{id: "message-123", want: true},
		{id: "消息-123", want: true},
		{id: "", want: false},
		{id: " leading", want: false},
		{id: "trailing ", want: false},
		{id: "message\x00id", want: false},
		{id: string([]byte{0xff}), want: false},
	} {
		if got := consumer.IsValidMessageID(test.id); got != test.want {
			t.Errorf("IsValidMessageID(%q) = %t, want %t", test.id, got, test.want)
		}
	}
}

func TestMessageContextRestoresRequestIdentityWithoutChangingOtherContextValues(t *testing.T) {
	type contextKey struct{}
	base := context.WithValue(context.Background(), contextKey{}, "trace")
	withRequest := consumer.MessageContext(base, consumer.Message{Headers: []consumer.Header{
		{Key: "unrelated", Value: []byte("ignored")},
		{Key: logger.RequestIDHeader, Value: []byte("request-123")},
	}})
	if got := logger.RequestID(withRequest); got != "request-123" {
		t.Fatalf("request identity = %q", got)
	}
	if got := withRequest.Value(contextKey{}); got != "trace" {
		t.Fatalf("context value = %v", got)
	}
	withoutRequest := consumer.MessageContext(base, consumer.Message{Headers: []consumer.Header{{Key: "unrelated"}}})
	if logger.RequestID(withoutRequest) != "" {
		t.Fatal("message without request identity inherited an unrelated identity")
	}
}

func TestMiddlewareChainPreservesOuterToInnerOrder(t *testing.T) {
	var calls []string
	handler := consumer.Chain(
		func(context.Context, consumer.Message) (consumer.Outcome, error) {
			calls = append(calls, "handler")
			return consumer.Success, nil
		},
		func(next consumer.Handler) consumer.Handler {
			return func(ctx context.Context, message consumer.Message) (consumer.Outcome, error) {
				calls = append(calls, "outer-before")
				outcome, err := next(ctx, message)
				calls = append(calls, "outer-after")
				return outcome, err
			}
		},
		func(next consumer.Handler) consumer.Handler {
			return func(ctx context.Context, message consumer.Message) (consumer.Outcome, error) {
				calls = append(calls, "inner-before")
				outcome, err := next(ctx, message)
				calls = append(calls, "inner-after")
				return outcome, err
			}
		},
	)
	if outcome, err := handler(context.Background(), consumer.Message{}); outcome != consumer.Success || err != nil {
		t.Fatalf("handler result = (%v, %v)", outcome, err)
	}
	want := []string{"outer-before", "inner-before", "handler", "inner-after", "outer-after"}
	if len(calls) != len(want) {
		t.Fatalf("middleware calls = %v", calls)
	}
	for index := range want {
		if calls[index] != want[index] {
			t.Fatalf("middleware calls = %v, want %v", calls, want)
		}
	}
}

func TestRecoveryConvertsHandlerPanicToRetryWithDiagnosticCause(t *testing.T) {
	wrapped := consumer.Recovery(func(context.Context, consumer.Message) (consumer.Outcome, error) {
		panic("broken decoder")
	})
	outcome, err := wrapped(context.Background(), consumer.Message{})
	if outcome != consumer.Retry || !errors.Is(err, consumer.ErrHandlerPanic) || !strings.Contains(err.Error(), "broken decoder") {
		t.Fatalf("panic result = (%v, %v)", outcome, err)
	}

	failure := errors.New("temporary failure")
	wrapped = consumer.Recovery(func(context.Context, consumer.Message) (consumer.Outcome, error) {
		return consumer.Retry, failure
	})
	outcome, err = wrapped(context.Background(), consumer.Message{})
	if outcome != consumer.Retry || !errors.Is(err, failure) {
		t.Fatalf("ordinary error changed by recovery: (%v, %v)", outcome, err)
	}
}

func TestRuntimeWindowBoundsSourceReadsForQueuePolicyAndBatching(t *testing.T) {
	handler := func(context.Context, consumer.Message) (consumer.Outcome, error) {
		return consumer.Success, nil
	}
	tests := []struct {
		name    string
		options []consumer.Option
		wantN   int
		batch   bool
	}{
		{name: "single-message runtime includes worker slots", wantN: 257},
		{name: "batch runtime includes each worker batch", options: []consumer.Option{consumer.WithWorkers(2), consumer.WithQueueCapacity(10), consumer.WithBatch(4, 0)}, wantN: 18, batch: true},
		{name: "rejecting queue reads only its admission capacity", options: []consumer.Option{consumer.WithQueueCapacity(10), consumer.WithFullQueue(consumer.Reject), consumer.WithBatch(4, 0)}, wantN: 10, batch: true},
		{name: "source window never exceeds dispatcher batch limit", options: []consumer.Option{consumer.WithQueueCapacity(1_000_000)}, wantN: 10_000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var runtime *consumer.Runtime
			var err error
			if test.batch {
				runtime, err = consumer.NewBatch(func(context.Context, []consumer.Message) []consumer.Result { return nil }, test.options...)
			} else {
				runtime, err = consumer.New(handler, test.options...)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close(context.Background())
			gotN, wait := runtime.Window()
			if gotN != test.wantN || wait != 0 {
				t.Fatalf("Window() = (%d, %s), want (%d, 0)", gotN, wait, test.wantN)
			}
		})
	}
	batchRuntime, err := consumer.NewBatch(func(context.Context, []consumer.Message) []consumer.Result { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer batchRuntime.Close(context.Background())
	if size, wait := batchRuntime.Window(); size != 384 || wait <= 0 {
		t.Fatalf("default batch window = (%d, %s)", size, wait)
	}
}
