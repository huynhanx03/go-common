package logger

import (
	"context"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Event writes one structured log event. Event-specific fields belong in
// metadata; durationMS is a top-level numeric field on completion events.
// Request and active span identity come from ctx.
func (l *Logger) Event(
	ctx context.Context,
	level zapcore.Level,
	message string,
	durationMS *int64,
	metadata ...zap.Field,
) {
	entry := l.base.Check(level, message)
	if entry == nil {
		return
	}
	fields := contextualFields(ctx, nil)
	if durationMS != nil {
		fields = append(fields, zap.Int64(fieldDurationMS, *durationMS))
	}
	if len(metadata) != 0 {
		fields = append(fields, zap.Dict(fieldMetadata, metadata...))
	}
	entry.Write(fields...)
}
