package ent

import "time"

// Config holds the database configuration
type Config struct {
	Driver          string
	DSN             string        // Data Source Name
	MaxOpenConns    int           // Maximum number of open connections
	MaxIdleConns    int           // Maximum number of idle connections
	ConnMaxLifetime time.Duration // Maximum amount of time a connection may be reused
	Debug           bool          // Enable debug mode
}
