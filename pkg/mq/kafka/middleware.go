package kafka

import (
	"context"

	"github.com/huynhanx03/go-common/pkg/mq/consumer"
)

// Chain wraps a handler with a list of middlewares
// The middlewares are applied in reverse order, so the first in the list is the outer-most
func Chain(h Handler, mws ...Middleware) Handler {
	if len(mws) == 0 {
		return h
	}

	// Apply in reverse order so mws[0] is the outermost
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Recovery catches panics in the handler and returns an error instead of crashing
func Recovery(next Handler) Handler {
	wrapped := consumer.Recovery(func(ctx context.Context, message consumer.Message) (consumer.Outcome, error) {
		if err := next(ctx, message.Key, message.Value); err != nil {
			return consumer.Retry, err
		}
		return consumer.Success, nil
	})
	return func(ctx context.Context, key, value []byte) error {
		_, err := wrapped(ctx, consumer.Message{Key: key, Value: value})
		return err
	}
}
