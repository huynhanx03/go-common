package kafka

import (
	"context"
	"fmt"
	"runtime/debug"
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
	return func(ctx context.Context, key, value []byte) (err error) {
		defer func() {
			if r := recover(); r != nil {
				stack := string(debug.Stack())
				err = fmt.Errorf("panic recovered in kafka handler: %v\nStack: %s", r, stack)
			}
		}()
		return next(ctx, key, value)
	}
}
