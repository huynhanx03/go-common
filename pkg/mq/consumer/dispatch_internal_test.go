package consumer

import (
	"context"
	"errors"
	"testing"
)

func TestNextKeepsConcurrentProcessRequestsInSeparateBatches(t *testing.T) {
	firstRequest := &request{ctx: context.Background()}
	secondRequest := &request{ctx: context.Background()}
	runtime := &Runtime{
		config: config{batchSize: 2},
		ctx:    context.Background(),
		queue: []*task{
			{request: secondRequest, message: Message{ID: "second"}},
			{request: firstRequest, message: Message{ID: "first"}},
		},
	}

	if got := runtime.next(firstRequest); got != 1 {
		t.Fatalf("next(first request) = %d, want index 1", got)
	}
	if got := runtime.next(secondRequest); got != 0 {
		t.Fatalf("next(second request) = %d, want index 0", got)
	}
}

func TestExecuteSkipsHandlerWhenRequestWasCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var handlerCalled bool
	runtime := &Runtime{
		ctx: context.Background(),
		handler: func(context.Context, Message) (Outcome, error) {
			handlerCalled = true
			return Success, nil
		},
	}
	batch := []*task{{request: &request{ctx: ctx}, message: Message{ID: "canceled"}}}

	results := runtime.execute(batch)
	if handlerCalled {
		t.Fatal("handler ran for a request canceled before execution")
	}
	if len(results) != 1 || results[0].Outcome != Retry || !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("execute result = %+v, want canceled retry", results)
	}
}
