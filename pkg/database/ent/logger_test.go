package ent

import (
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/huynhanx03/go-common/pkg/logger"
)

// fakeDialectDriver is a minimal dialect.Driver whose Exec/Query return a
// canned error.
type fakeDialectDriver struct {
	err error
}

func (d *fakeDialectDriver) Exec(context.Context, string, any, any) error  { return d.err }
func (d *fakeDialectDriver) Query(context.Context, string, any, any) error { return d.err }
func (d *fakeDialectDriver) Tx(context.Context) (dialect.Tx, error)        { return nil, d.err }
func (d *fakeDialectDriver) Close() error                                  { return nil }
func (d *fakeDialectDriver) Dialect() string                               { return dialect.MySQL }

func observedCtx(t *testing.T) (context.Context, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	return logger.WithContext(context.Background(), zap.New(core)), logs
}

func TestWrapLoggingSlowQuery(t *testing.T) {
	ctx, logs := observedCtx(t)
	drv := WrapLogging(&fakeDialectDriver{}, time.Nanosecond)

	if err := drv.Query(ctx, "SELECT 1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := logs.FilterMessage("slow query").Len(); got != 1 {
		t.Fatalf("slow query logs = %d, want 1", got)
	}
}

func TestWrapLoggingError(t *testing.T) {
	ctx, logs := observedCtx(t)
	drv := WrapLogging(&fakeDialectDriver{err: errors.New("syntax error")}, 0)

	if err := drv.Exec(ctx, "UPDATE x", nil, nil); err == nil {
		t.Fatal("driver error must propagate")
	}
	if got := logs.FilterMessage("query failed").Len(); got != 1 {
		t.Fatalf("error logs = %d, want 1", got)
	}
}

func TestWrapLoggingIgnoresCanceled(t *testing.T) {
	ctx, logs := observedCtx(t)
	drv := WrapLogging(&fakeDialectDriver{err: context.Canceled}, 0)

	_ = drv.Query(ctx, "SELECT 1", nil, nil)
	if got := logs.Len(); got != 0 {
		t.Fatalf("canceled queries must not be logged, got %d entries", got)
	}
}

func TestWrapLoggingDebugCarriesArgs(t *testing.T) {
	ctx, logs := observedCtx(t)
	drv := WrapLogging(&fakeDialectDriver{}, time.Hour)

	if err := drv.Query(ctx, "SELECT 1", []any{1}, nil); err != nil {
		t.Fatal(err)
	}
	entries := logs.FilterMessage("query").All()
	if len(entries) != 1 || entries[0].Level != zapcore.DebugLevel {
		t.Fatalf("want exactly one Debug entry, got %+v", entries)
	}
}
