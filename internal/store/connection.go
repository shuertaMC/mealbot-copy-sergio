package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	defaultMaxOpenConnections = 16
	defaultMaxIdleConnections = 4
	defaultConnectionLifetime = 30 * time.Minute
	defaultConnectionIdleTime  = 5 * time.Minute
	defaultPingTimeout         = 5 * time.Second
	defaultOperationTimeout    = 10 * time.Second
)

var (
	ErrDatabaseURLRequired = errors.New("DATABASE_URL is required")
	ErrNilDatabase         = errors.New("database handle is required")
)

// ConnectionOptions controls the database/sql pool and bounded startup work.
// Zero values select conservative production defaults.
type ConnectionOptions struct {
	MaxOpenConnections int
	MaxIdleConnections int
	ConnectionLifetime time.Duration
	ConnectionIdleTime  time.Duration
	PingTimeout         time.Duration
}

// DefaultConnectionOptions returns the production pool defaults.
func DefaultConnectionOptions() ConnectionOptions {
	return ConnectionOptions{
		MaxOpenConnections: defaultMaxOpenConnections,
		MaxIdleConnections: defaultMaxIdleConnections,
		ConnectionLifetime: defaultConnectionLifetime,
		ConnectionIdleTime:  defaultConnectionIdleTime,
		PingTimeout:         defaultPingTimeout,
	}
}

func normalizeConnectionOptions(options ConnectionOptions) ConnectionOptions {
	defaults := DefaultConnectionOptions()
	if options.MaxOpenConnections <= 0 {
		options.MaxOpenConnections = defaults.MaxOpenConnections
	}
	if options.MaxIdleConnections <= 0 {
		options.MaxIdleConnections = defaults.MaxIdleConnections
	}
	if options.MaxIdleConnections > options.MaxOpenConnections {
		options.MaxIdleConnections = options.MaxOpenConnections
	}
	if options.ConnectionLifetime <= 0 {
		options.ConnectionLifetime = defaults.ConnectionLifetime
	}
	if options.ConnectionIdleTime <= 0 {
		options.ConnectionIdleTime = defaults.ConnectionIdleTime
	}
	if options.PingTimeout <= 0 {
		options.PingTimeout = defaults.PingTimeout
	}
	return options
}

// Open opens and verifies a pgx/v5 database/sql pool using only databaseURL.
// It does not read the environment and does not log the URL.
func Open(ctx context.Context, databaseURL string, options ...ConnectionOptions) (*sql.DB, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, ErrDatabaseURLRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	settings := ConnectionOptions{}
	if len(options) > 0 {
		settings = options[0]
	}
	settings = normalizeConnectionOptions(settings)

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL connection: %w", err)
	}
	db.SetMaxOpenConns(settings.MaxOpenConnections)
	db.SetMaxIdleConns(settings.MaxIdleConnections)
	db.SetConnMaxLifetime(settings.ConnectionLifetime)
	db.SetConnMaxIdleTime(settings.ConnectionIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, settings.PingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return db, nil
}
