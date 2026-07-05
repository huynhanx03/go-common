package ent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"go.uber.org/zap"

	"github.com/huynhanx03/go-common/pkg/logger"
)

// DefaultSlowThreshold marks statements as slow when WrapLogging is given no
// threshold.
const DefaultSlowThreshold = 200 * time.Millisecond

// WrapLogging decorates a driver so every statement is timed and logged
// through the context logger — lines carry the request's cid automatically:
//
//   - failed statements → Error
//   - statements slower than slowThreshold (0 = 200ms default) → Warn
//   - everything → Debug, with args
//
// There is no "enable query log" switch: the logger's level is the switch.
// Dev runs at Debug and sees every statement; prod runs at Info and the
// Debug lines — the only place args appear — are dropped before formatting,
// so nothing sensitive is ever written.
//
//	drv, _ := ent.NewDriver(cfg)
//	client := gen.NewClient(gen.Driver(ent.WrapLogging(drv, 0)))
func WrapLogging(drv dialect.Driver, slowThreshold time.Duration) dialect.Driver {
	if slowThreshold <= 0 {
		slowThreshold = DefaultSlowThreshold
	}
	return &logDriver{Driver: drv, slow: slowThreshold}
}

type logDriver struct {
	dialect.Driver
	slow time.Duration
}

func (d *logDriver) Exec(ctx context.Context, query string, args, v any) error {
	start := time.Now()
	err := d.Driver.Exec(ctx, query, args, v)
	logQuery(ctx, d.slow, query, args, time.Since(start), err)
	return err
}

func (d *logDriver) Query(ctx context.Context, query string, args, v any) error {
	start := time.Now()
	err := d.Driver.Query(ctx, query, args, v)
	logQuery(ctx, d.slow, query, args, time.Since(start), err)
	return err
}

func (d *logDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	t, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}
	return &logTx{Tx: t, slow: d.slow}, nil
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
	t, err := drv.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &logTx{Tx: t, slow: d.slow}, nil
}

type logTx struct {
	dialect.Tx
	slow time.Duration
}

func (t *logTx) Exec(ctx context.Context, query string, args, v any) error {
	start := time.Now()
	err := t.Tx.Exec(ctx, query, args, v)
	logQuery(ctx, t.slow, query, args, time.Since(start), err)
	return err
}

func (t *logTx) Query(ctx context.Context, query string, args, v any) error {
	start := time.Now()
	err := t.Tx.Query(ctx, query, args, v)
	logQuery(ctx, t.slow, query, args, time.Since(start), err)
	return err
}

func logQuery(ctx context.Context, slow time.Duration, query string, args any, took time.Duration, err error) {
	switch {
	case err != nil:
		// A canceled request aborting its query is request noise, not a
		// database problem.
		if errors.Is(err, context.Canceled) {
			return
		}
		logger.FromContext(ctx).Error("query failed",
			zap.String("query", query),
			zap.Duration("took", took),
			zap.Error(err),
		)
	case took >= slow:
		logger.FromContext(ctx).Warn("slow query",
			zap.String("query", query),
			zap.Duration("took", took),
		)
	default:
		logger.FromContext(ctx).Debug("query",
			zap.String("query", query),
			zap.Any("args", args),
			zap.Duration("took", took),
		)
	}
}
