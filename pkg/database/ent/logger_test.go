package ent

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
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
	const threshold = time.Nanosecond
	drv := WrapLogging(&fakeDialectDriver{}, threshold).(*logDriver)
	startedAt := time.Unix(0, 0)
	clockCalls := 0
	drv.now = func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			return startedAt
		}
		return startedAt.Add(threshold)
	}

	if err := drv.Query(ctx, "SELECT 1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := logs.FilterMessage("slow query").Len(); got != 1 {
		t.Fatalf("slow query logs = %d, want 1", got)
	}
}

func TestWrapLoggingErrorDoesNotExposeStatementArgumentsOrErrorText(t *testing.T) {
	ctx, logs := observedCtx(t)
	drv := WrapLogging(
		&fakeDialectDriver{err: errors.New("password=hunter2 token=top-secret")},
		0,
	)

	if err := drv.Exec(
		ctx,
		"UPDATE credentials SET password = 'literal-secret'",
		[]any{"argument-secret"},
		nil,
	); err == nil {
		t.Fatal("driver error must propagate")
	}
	entries := logs.FilterMessage("query failed").All()
	if len(entries) != 1 {
		t.Fatalf("error logs = %d, want 1", len(entries))
	}
	for key, value := range entries[0].ContextMap() {
		text := fmt.Sprint(value)
		if key == "query" || key == "args" || key == "error" ||
			strings.Contains(text, "hunter2") ||
			strings.Contains(text, "top-secret") ||
			strings.Contains(text, "literal-secret") ||
			strings.Contains(text, "argument-secret") {
			t.Fatalf("sensitive database detail leaked through %q: %v", key, value)
		}
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

func TestWrapLoggingDebugUsesStableSafeStatementIdentity(t *testing.T) {
	ctx, logs := observedCtx(t)
	drv := WrapLogging(&fakeDialectDriver{}, time.Hour)
	const query = "SELECT * FROM credentials WHERE password = 'literal-secret'"

	if err := drv.Query(ctx, query, []any{"argument-secret"}, nil); err != nil {
		t.Fatal(err)
	}
	entries := logs.FilterMessage("query").All()
	if len(entries) != 1 || entries[0].Level != zapcore.DebugLevel {
		t.Fatalf("want exactly one Debug entry, got %+v", entries)
	}
	fields := entries[0].ContextMap()
	if fields["operation"] != "SELECT" ||
		fields["query_hash"] != "a8d8b46e2606486f57d73a3019542ec5cef7933e3069361739f600898cc864dd" {
		t.Fatalf("safe statement identity = %+v", fields)
	}
	if _, exists := fields["query"]; exists {
		t.Fatal("raw query text was logged")
	}
	if _, exists := fields["args"]; exists {
		t.Fatal("raw query arguments were logged")
	}
}

func TestWrapObservedLoggingReportsOnlyBoundedStatementMetadata(t *testing.T) {
	secretErr := errors.New("password=hunter2")
	observations := make([]StatementObservation, 0, 4)
	drv := WrapObservedLogging(
		&fakeDialectDriver{err: secretErr},
		time.Hour,
		func(_ context.Context, observation StatementObservation) {
			observations = append(observations, observation)
		},
	)

	_ = drv.Query(context.Background(), "SELECT secret FROM credentials", nil, nil)
	_ = drv.Exec(context.Background(), "UPDATE credentials SET secret = ?", nil, nil)
	_ = drv.Exec(context.Background(), "WITH candidate AS (SELECT 1) DELETE FROM jobs", nil, nil)
	_, _ = drv.Tx(context.Background())

	wantClasses := []StatementClass{
		StatementClassRead,
		StatementClassWrite,
		StatementClassOther,
		StatementClassTransaction,
	}
	if len(observations) != len(wantClasses) {
		t.Fatalf("observations = %d, want %d", len(observations), len(wantClasses))
	}
	for index, wantClass := range wantClasses {
		observation := observations[index]
		if observation.Class != wantClass ||
			observation.Duration < 0 ||
			!errors.Is(observation.Err, secretErr) {
			t.Fatalf("observation[%d] = %+v, want class=%q error=%v", index, observation, wantClass, secretErr)
		}
	}
}

func TestWrapObservedLoggingObserverCannotBreakDatabaseCall(t *testing.T) {
	drv := WrapObservedLogging(
		&fakeDialectDriver{},
		time.Hour,
		func(context.Context, StatementObservation) { panic("telemetry failed") },
	)

	if err := drv.Query(context.Background(), "SELECT 1", nil, nil); err != nil {
		t.Fatalf("database result changed by observer panic: %v", err)
	}
}

func TestUnwrapLoggingReturnsOriginalDriver(t *testing.T) {
	original := &fakeDialectDriver{}
	wrapped := WrapLogging(WrapObservedLogging(original, 0, nil), 0)

	if got := UnwrapLogging(wrapped); got != original {
		t.Fatalf("UnwrapLogging() = %T %p, want %T %p", got, got, original, original)
	}
}

// contextDialectDriver adds the context statement variants that the generated
// Ent clients type-assert for when application code runs raw SQL.
type contextDialectDriver struct {
	fakeDialectDriver
	tx        dialect.Tx
	execCalls int
	execQuery string
}

func (d *contextDialectDriver) ExecContext(
	_ context.Context,
	query string,
	_ ...any,
) (sql.Result, error) {
	d.execCalls++
	d.execQuery = query
	return driver.RowsAffected(1), nil
}

func (d *contextDialectDriver) QueryContext(
	context.Context,
	string,
	...any,
) (*sql.Rows, error) {
	return nil, errNoRows
}

func (d *contextDialectDriver) Tx(context.Context) (dialect.Tx, error) { return d.tx, nil }

type contextDialectTx struct {
	execCalls int
	execQuery string
}

func (t *contextDialectTx) Exec(context.Context, string, any, any) error  { return nil }
func (t *contextDialectTx) Query(context.Context, string, any, any) error { return nil }
func (t *contextDialectTx) Commit() error                                 { return nil }
func (t *contextDialectTx) Rollback() error                               { return nil }

func (t *contextDialectTx) ExecContext(
	_ context.Context,
	query string,
	_ ...any,
) (sql.Result, error) {
	t.execCalls++
	t.execQuery = query
	return driver.RowsAffected(1), nil
}

func (t *contextDialectTx) QueryContext(
	context.Context,
	string,
	...any,
) (*sql.Rows, error) {
	return nil, errNoRows
}

var errNoRows = errors.New("no rows")

// Raw statements such as PostgreSQL advisory locks reach the wrapper through a
// type assertion that embedding dialect.Driver/dialect.Tx does not satisfy.
func TestWrapLoggingForwardsContextStatementsOnDriverAndTransaction(t *testing.T) {
	const statement = "SELECT pg_advisory_xact_lock($1)"
	transaction := &contextDialectTx{}
	original := &contextDialectDriver{tx: transaction}
	wrapped := WrapLogging(original, 0)

	execer, ok := wrapped.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	})
	if !ok {
		t.Fatal("logging driver does not expose ExecContext")
	}
	if _, err := execer.ExecContext(context.Background(), statement, "lock"); err != nil {
		t.Fatalf("driver ExecContext: %v", err)
	}
	if original.execCalls != 1 || original.execQuery != statement {
		t.Fatalf("driver forwarding = %d %q", original.execCalls, original.execQuery)
	}

	loggedTx, err := wrapped.Tx(context.Background())
	if err != nil {
		t.Fatalf("Tx: %v", err)
	}
	txExecer, ok := loggedTx.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	})
	if !ok {
		t.Fatal("logging transaction does not expose ExecContext")
	}
	if _, err := txExecer.ExecContext(context.Background(), statement, "lock"); err != nil {
		t.Fatalf("transaction ExecContext: %v", err)
	}
	if transaction.execCalls != 1 || transaction.execQuery != statement {
		t.Fatalf("transaction forwarding = %d %q", transaction.execCalls, transaction.execQuery)
	}

	if _, ok := loggedTx.(interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}); !ok {
		t.Fatal("logging transaction does not expose QueryContext")
	}
}
