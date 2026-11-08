//go:build integration

// Package ent provides PostgreSQL helpers for Ent integration tests.
package ent

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/huynhanx03/go-common/pkg/testutil/containers"
	_ "github.com/lib/pq"
)

const postgresConnectionTimeout = 10 * time.Second

// OpenPostgres starts an isolated PostgreSQL container and returns its SQL
// pool and Ent driver. The helper closes the shared pool during test cleanup.
func OpenPostgres(t testing.TB) (*sql.DB, dialect.Driver) {
	t.Helper()

	db, err := sql.Open("postgres", containers.StartPostgres(t))
	if err != nil {
		t.Fatalf("open test PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test PostgreSQL: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), postgresConnectionTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect to test PostgreSQL: %v", err)
	}

	return db, entsql.OpenDB(dialect.Postgres, db)
}
