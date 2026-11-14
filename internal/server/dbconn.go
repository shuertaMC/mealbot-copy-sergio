// Package server holds small helpers shared by the HTTP API and the CLI modes:
// database connection setup, JSONB column support and JSON message responses.
package server

import (
	"context"
	"database/sql"
	"errors"
	"time"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Driver is the database/sql driver name registered by pgx's stdlib package.
const Driver = "pgx"

// ErrNoDatabaseURL is returned when no database URL was configured.
var ErrNoDatabaseURL = errors.New("DATABASE_URL is not set")

// CreateDBConnection opens a pooled connection to the Postgres database at
// databaseURL. sql.Open does not dial the database; callers that want to fail
// fast should follow up with db.PingContext.
func CreateDBConnection(databaseURL string) (*sql.DB, error) {
	if databaseURL == "" {
		return nil, ErrNoDatabaseURL
	}

	db, err := sql.Open(Driver, databaseURL)
	if err != nil {
		// db is nil when sql.Open fails, so there is nothing to close
		return nil, err
	}

	return db, nil
}

// Ping verifies the database is reachable, giving up after timeout.
func Ping(ctx context.Context, db *sql.DB, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return db.PingContext(ctx)
}
