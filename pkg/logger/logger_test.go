package logger

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestLoggerMethodsAndDerivedLoggers(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	base := Wrap(zap.New(core))
	derived := base.Named("component").With(zap.String("fixed", "yes"))
	ctx := WithCorrelationID(context.Background(), "job-7")
	derived.Debug(ctx, "debug")
	derived.Info(ctx, "info")
	derived.Warn(ctx, "warn")
	derived.Error(ctx, "error")
	if got := observed.Len(); got != 4 {
		t.Fatalf("entries = %d, want 4", got)
	}
	for _, entry := range observed.All() {
		fields := entry.ContextMap()
		if fields["fixed"] != "yes" || fields[fieldCorrelation] != "job-7" {
			t.Errorf("entry %q fields = %#v", entry.Message, fields)
		}
		if entry.LoggerName != "component" {
			t.Errorf("logger name = %q", entry.LoggerName)
		}
	}
}

func TestLoggerPanicAndDPanic(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	log := Wrap(zap.New(core))
	func() {
		defer func() {
			if recover() != "panic message" {
				t.Fatal("Panic did not panic with the log message")
			}
		}()
		log.Panic(context.Background(), "panic message")
	}()
	log.DPanic(context.Background(), "development panic")
	if observed.Len() != 2 {
		t.Fatalf("entries = %d, want panic and dpanic entries", observed.Len())
	}
}

func TestWrappedLoggerWithoutDynamicLevel(t *testing.T) {
	log := Wrap(zap.NewNop())
	if log.Core() == nil {
		t.Fatal("Core() returned nil")
	}
	if err := log.SetLevel("debug"); err != ErrInvalidLogger {
		t.Fatalf("SetLevel on zero AtomicLevel error = %v, want ErrInvalidLogger", err)
	}
	if log.CurrentLevel() != "" {
		t.Fatal("CurrentLevel on zero AtomicLevel should be unavailable")
	}
}
