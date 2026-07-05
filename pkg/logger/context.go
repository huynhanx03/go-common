package logger

import (
	"context"

	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/cid"
)

type ctxKey struct{}

// WithContext stores a zap.Logger in the context.
func WithContext(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext retrieves the zap.Logger from context. Request-scoped loggers
// stored by middleware arrive already tagged with the correlation ID; the
// global-logger fallback gets the context's cid attached on the fly, so log
// lines correlate no matter which path they took.
func FromContext(ctx context.Context) *zap.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*zap.Logger); ok {
		return l
	}

	l := zap.L()
	if id := cid.FromContext(ctx); id != "" {
		l = l.With(zap.String("cid", id))
	}
	return l
}
