package logger

import (
	"context"
	"errors"
	"net/http"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func (log *Logger) Named(name string) *Logger {
	result := *log
	result.base = log.base.Named(name)
	return &result
}

func (log *Logger) With(fields ...zap.Field) *Logger {
	result := *log
	result.base = log.base.With(fields...)
	return &result
}

func contextualFields(ctx context.Context, fields []zap.Field) []zap.Field {
	return append(contextLogFields(nonNilContext(ctx)), fields...)
}

func (log *Logger) Debug(ctx context.Context, message string, fields ...zap.Field) {
	log.base.Debug(message, contextualFields(ctx, fields)...)
}
func (log *Logger) Info(ctx context.Context, message string, fields ...zap.Field) {
	log.base.Info(message, contextualFields(ctx, fields)...)
}
func (log *Logger) Warn(ctx context.Context, message string, fields ...zap.Field) {
	log.base.Warn(message, contextualFields(ctx, fields)...)
}
func (log *Logger) Error(ctx context.Context, message string, fields ...zap.Field) {
	log.base.Error(message, contextualFields(ctx, fields)...)
}
func (log *Logger) DPanic(ctx context.Context, message string, fields ...zap.Field) {
	log.base.DPanic(message, contextualFields(ctx, fields)...)
}
func (log *Logger) Panic(ctx context.Context, message string, fields ...zap.Field) {
	log.base.Panic(message, contextualFields(ctx, fields)...)
}
func (log *Logger) Fatal(ctx context.Context, message string, fields ...zap.Field) {
	log.base.Fatal(message, contextualFields(ctx, fields)...)
}

// Core returns the underlying Zap core for compatibility with existing users.
func (log *Logger) Core() zapcore.Core { return log.base.Core() }

func (log *Logger) Sync() error {
	if log == nil || log.base == nil {
		return ErrInvalidLogger
	}
	err := log.base.Sync()
	if err != nil && isStdoutSyncErr(err) {
		return nil
	}
	return err
}

func isStdoutSyncErr(err error) bool {
	return errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.EBADF) || errors.Is(err, syscall.ENOTSUP)
}

func (log *Logger) SetLevel(level string) (resultErr error) {
	if log == nil || log.base == nil {
		return ErrInvalidLogger
	}
	defer func() {
		if recover() != nil {
			resultErr = ErrInvalidLogger
		}
	}()
	var parsed zapcore.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		return err
	}
	log.level.SetLevel(parsed)
	return nil
}

func (log *Logger) Level() zapcore.Level {
	level, valid := log.currentLevel()
	if !valid {
		return zapcore.InfoLevel
	}
	return level
}

func (log *Logger) CurrentLevel() string {
	level, valid := log.currentLevel()
	if !valid {
		return ""
	}
	return level.String()
}

// LevelHandler exposes Zap's level handler. Mount only on a protected endpoint.
// Deprecated: prefer an authenticated, authorized, audited, time-bounded control surface.
func (log *Logger) LevelHandler() http.Handler {
	if _, valid := log.currentLevel(); !valid {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, responseUnavailable, http.StatusServiceUnavailable)
		})
	}
	return log.level
}

func (log *Logger) currentLevel() (level zapcore.Level, valid bool) {
	if log == nil || log.base == nil {
		return zapcore.InfoLevel, false
	}
	defer func() {
		if recover() != nil {
			level, valid = zapcore.InfoLevel, false
		}
	}()
	return log.level.Level(), true
}
