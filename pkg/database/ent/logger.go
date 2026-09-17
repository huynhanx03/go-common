package ent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/logger"
)

// DefaultSlowThreshold marks statements as slow when WrapLogging is given no
// threshold.
const DefaultSlowThreshold = 200 * time.Millisecond

// StatementClass is a deliberately small, transport-safe classification. It
// lets applications observe database latency without receiving statement text,
// arguments, table names, or any other unbounded/sensitive metadata.
type StatementClass string

const (
	StatementClassRead        StatementClass = "read"
	StatementClassWrite       StatementClass = "write"
	StatementClassTransaction StatementClass = "transaction"
	StatementClassOther       StatementClass = "other"
)

// StatementObservation describes one completed driver call. Err is provided
// for outcome classification only; observers must never render its text into
// metrics or logs.
type StatementObservation struct {
	Class    StatementClass
	Duration time.Duration
	Err      error
}

// StatementObserver receives bounded statement metadata after the underlying
// driver call completes. Observer panics are recovered so instrumentation can
// never change an application's database result.
type StatementObserver func(context.Context, StatementObservation)

// WrapLogging decorates a driver so every statement is timed and logged
// through the context logger — lines carry the request's cid automatically:
//
//   - failed statements → Error
//   - statements slower than slowThreshold (0 = 200ms default) → Warn
//   - everything → Debug
//
// Every entry carries only the SQL operation and a stable SHA-256 statement
// identity. Raw SQL, arguments, and database error text are deliberately never
// logged: any of them may contain credentials, personal data, source code, or
// opaque payloads even in development.
//
//	drv, _ := ent.NewDriver(cfg)
//	client := gen.NewClient(gen.Driver(ent.WrapLogging(drv, 0)))
func WrapLogging(drv dialect.Driver, slowThreshold time.Duration) dialect.Driver {
	return WrapObservedLogging(drv, slowThreshold, nil)
}

// WrapObservedLogging is WrapLogging plus a generic completion observer. The
// observer API intentionally omits SQL and arguments, keeping this reusable in
// services with stricter data handling rules.
func WrapObservedLogging(
	drv dialect.Driver,
	slowThreshold time.Duration,
	observer StatementObserver,
) dialect.Driver {
	if slowThreshold <= 0 {
		slowThreshold = DefaultSlowThreshold
	}
	return &logDriver{
		Driver:   drv,
		slow:     slowThreshold,
		now:      time.Now,
		observer: observer,
	}
}

type logDriver struct {
	dialect.Driver
	slow     time.Duration
	now      func() time.Time
	observer StatementObserver
}

func (d *logDriver) Exec(ctx context.Context, query string, args, v any) error {
	start := d.now()
	err := d.Driver.Exec(ctx, query, args, v)
	took := d.now().Sub(start)
	logQuery(ctx, d.slow, query, args, took, err)
	notifyObserver(ctx, d.observer, StatementObservation{
		Class: statementClass(query), Duration: took, Err: err,
	})
	return err
}

func (d *logDriver) Query(ctx context.Context, query string, args, v any) error {
	start := d.now()
	err := d.Driver.Query(ctx, query, args, v)
	took := d.now().Sub(start)
	logQuery(ctx, d.slow, query, args, took, err)
	notifyObserver(ctx, d.observer, StatementObservation{
		Class: statementClass(query), Duration: took, Err: err,
	})
	return err
}

// ExecContext and QueryContext are not part of dialect.Driver, but the
// generated Ent clients type-assert for them whenever application code runs raw
// SQL. Embedding dialect.Driver does not promote them, so without these
// forwards every raw statement fails once logging is wrapped around the driver.
func (d *logDriver) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	drv, ok := d.Driver.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	})
	if !ok {
		return nil, fmt.Errorf("ent: driver %T does not support ExecContext", d.Driver)
	}
	start := d.now()
	result, err := drv.ExecContext(ctx, query, args...)
	took := d.now().Sub(start)
	logQuery(ctx, d.slow, query, args, took, err)
	notifyObserver(ctx, d.observer, StatementObservation{
		Class: statementClass(query), Duration: took, Err: err,
	})
	return result, err
}

func (d *logDriver) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	drv, ok := d.Driver.(interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	})
	if !ok {
		return nil, fmt.Errorf("ent: driver %T does not support QueryContext", d.Driver)
	}
	start := d.now()
	rows, err := drv.QueryContext(ctx, query, args...)
	took := d.now().Sub(start)
	logQuery(ctx, d.slow, query, args, took, err)
	notifyObserver(ctx, d.observer, StatementObservation{
		Class: statementClass(query), Duration: took, Err: err,
	})
	return rows, err
}

func (d *logDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	start := d.now()
	t, err := d.Driver.Tx(ctx)
	took := d.now().Sub(start)
	notifyObserver(ctx, d.observer, StatementObservation{
		Class: StatementClassTransaction, Duration: took, Err: err,
	})
	if err != nil {
		return nil, err
	}
	return &logTx{Tx: t, slow: d.slow, now: d.now, observer: d.observer}, nil
}

// BeginTx forwards transaction options when the underlying driver supports
// them (entsql.Driver does); the generated BeginTx API requires it.
func (d *logDriver) BeginTx(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	drv, ok := d.Driver.(interface {
		BeginTx(context.Context, *sql.TxOptions) (dialect.Tx, error)
	})
	if !ok {
		return nil, fmt.Errorf("ent: driver %T does not support BeginTx", d.Driver)
	}
	start := d.now()
	t, err := drv.BeginTx(ctx, opts)
	took := d.now().Sub(start)
	notifyObserver(ctx, d.observer, StatementObservation{
		Class: StatementClassTransaction, Duration: took, Err: err,
	})
	if err != nil {
		return nil, err
	}
	return &logTx{Tx: t, slow: d.slow, now: d.now, observer: d.observer}, nil
}

// UnwrapLogging removes any number of logging wrappers and returns the
// original dialect driver. This is useful for APIs such as entsql.OpenDB that
// need the concrete driver while the generated Ent client keeps the wrapper.
func UnwrapLogging(drv dialect.Driver) dialect.Driver {
	for {
		wrapped, ok := drv.(*logDriver)
		if !ok || wrapped == nil || wrapped.Driver == nil {
			return drv
		}
		drv = wrapped.Driver
	}
}

type logTx struct {
	dialect.Tx
	slow     time.Duration
	now      func() time.Time
	observer StatementObserver
}

func (t *logTx) Exec(ctx context.Context, query string, args, v any) error {
	start := t.now()
	err := t.Tx.Exec(ctx, query, args, v)
	took := t.now().Sub(start)
	logQuery(ctx, t.slow, query, args, took, err)
	notifyObserver(ctx, t.observer, StatementObservation{
		Class: statementClass(query), Duration: took, Err: err,
	})
	return err
}

func (t *logTx) Query(ctx context.Context, query string, args, v any) error {
	start := t.now()
	err := t.Tx.Query(ctx, query, args, v)
	took := t.now().Sub(start)
	logQuery(ctx, t.slow, query, args, took, err)
	notifyObserver(ctx, t.observer, StatementObservation{
		Class: statementClass(query), Duration: took, Err: err,
	})
	return err
}

// dialect.Tx does not declare the context variants, so raw transactional SQL
// such as an advisory lock reaches the wrapper through a type assertion and
// needs these forwards to survive the logging layer.
func (t *logTx) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	tx, ok := t.Tx.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	})
	if !ok {
		return nil, fmt.Errorf("ent: transaction %T does not support ExecContext", t.Tx)
	}
	start := t.now()
	result, err := tx.ExecContext(ctx, query, args...)
	took := t.now().Sub(start)
	logQuery(ctx, t.slow, query, args, took, err)
	notifyObserver(ctx, t.observer, StatementObservation{
		Class: statementClass(query), Duration: took, Err: err,
	})
	return result, err
}

func (t *logTx) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	tx, ok := t.Tx.(interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	})
	if !ok {
		return nil, fmt.Errorf("ent: transaction %T does not support QueryContext", t.Tx)
	}
	start := t.now()
	rows, err := tx.QueryContext(ctx, query, args...)
	took := t.now().Sub(start)
	logQuery(ctx, t.slow, query, args, took, err)
	notifyObserver(ctx, t.observer, StatementObservation{
		Class: statementClass(query), Duration: took, Err: err,
	})
	return rows, err
}

func notifyObserver(
	ctx context.Context,
	observer StatementObserver,
	observation StatementObservation,
) {
	if observer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	observer(ctx, observation)
}

func statementClass(query string) StatementClass {
	switch sqlOperation(query) {
	case "SELECT", "SHOW", "EXPLAIN", "DESCRIBE", "DESC":
		return StatementClassRead
	case "INSERT", "UPDATE", "DELETE", "MERGE", "REPLACE", "UPSERT":
		return StatementClassWrite
	default:
		return StatementClassOther
	}
}

func logQuery(ctx context.Context, slow time.Duration, query string, _ any, took time.Duration, err error) {
	fields := safeQueryFields(query, took)
	switch {
	case err != nil:
		// A canceled request aborting its query is request noise, not a
		// database problem.
		if errors.Is(err, context.Canceled) {
			return
		}
		fields = append(fields, zap.String("error_type", fmt.Sprintf("%T", err)))
		logger.FromContext(ctx).Error("query failed", fields...)
	case took >= slow:
		logger.FromContext(ctx).Warn("slow query", fields...)
	default:
		logger.FromContext(ctx).Debug("query", fields...)
	}
}

func safeQueryFields(query string, took time.Duration) []zap.Field {
	digest := sha256.Sum256([]byte(query))
	return []zap.Field{
		zap.String("operation", sqlOperation(query)),
		zap.String("query_hash", hex.EncodeToString(digest[:])),
		zap.Duration("took", took),
	}
}

func sqlOperation(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 || len(fields[0]) > 16 {
		return "UNKNOWN"
	}
	operation := strings.ToUpper(fields[0])
	for index := 0; index < len(operation); index++ {
		if operation[index] < 'A' || operation[index] > 'Z' {
			return "UNKNOWN"
		}
	}
	return operation
}
