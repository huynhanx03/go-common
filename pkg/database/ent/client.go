package ent

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

// NewDriver creates a new Ent SQL driver and validates the connection.
func NewDriver(cfg Config) (*entsql.Driver, error) {
	// Validate driver
	switch cfg.Driver {
	case DriverMySQL, DriverPostgres:
		// Drivers are registered via import
	default:
		return nil, fmt.Errorf("unsupported driver: %s", cfg.Driver)
	}

	// Open connection
	db, err := sql.Open(cfg.Driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Create Ent driver
	drv := entsql.OpenDB(cfg.Driver, db)

	return drv, nil
}
