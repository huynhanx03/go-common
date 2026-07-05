package logger

import (
	"context"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/huynhanx03/go-common/pkg/cid"
	"github.com/huynhanx03/go-common/pkg/settings"
)

func TestFromContextReturnsStoredLogger(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	ctx := WithContext(context.Background(), zap.New(core))

	FromContext(ctx).Info("stored")

	if logs.Len() != 1 || logs.All()[0].Message != "stored" {
		t.Errorf("expected entry via stored logger, got %v", logs.All())
	}
}

func TestFromContextFallbackAttachesCID(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	ctx := cid.WithContext(context.Background(), "cid-777")
	FromContext(ctx).Info("via fallback")

	fields := logs.All()[0].ContextMap()
	if fields["cid"] != "cid-777" {
		t.Errorf("cid field = %v, want cid-777", fields["cid"])
	}
}

func TestSyncIgnoresStdoutError(t *testing.T) {
	l := NewLogger(LoggerConfig{Mode: settings.EnvProd})
	l.Info("before sync")

	// Under `go test`, stdout is a pipe/char device whose fsync fails —
	// exactly the case Sync must swallow.
	if err := l.Sync(); err != nil {
		t.Errorf("Sync() = %v, want nil", err)
	}
}
