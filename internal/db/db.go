// Package db provides the PostgreSQL connection helpers, the JSONB column type,
// the duplicate-key error text and the small Querier interface that the rest of the
// code depends on (so it can be mocked with go-sqlmock).
package db

import (
	"database/sql"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"github.com/johnamadeo/mealbot/internal/config"
)

const (
	// DriverName is the database/sql driver name registered by pgx's stdlib package.
	DriverName = "pgx"
	// DuplicateKeyErr : Error message when user tries to insert row with duplicate key
	DuplicateKeyErr = "duplicate key value violates unique constraint"
)

// Querier is the subset of *sql.DB used by the application. *sql.DB and
// sqlmock connections satisfy it.
type Querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

var _ Querier = (*sql.DB)(nil)

// Open creates a *sql.DB for the configured database. Like sql.Open it does not
// establish a connection until first use.
func Open(cfg config.DB) (*sql.DB, error) {
	return sql.Open(DriverName, DataSource(cfg))
}

// DataSource returns the connection string for cfg. A non-empty URL (such as
// Heroku's DATABASE_URL) wins; otherwise a key/value DSN is assembled.
func DataSource(cfg config.DB) string {
	if cfg.URL != "" {
		return cfg.URL
	}

	parts := []string{}
	add := func(key, value string) {
		if value != "" {
			parts = append(parts, key+"="+quoteDSNValue(value))
		}
	}
	add("host", cfg.Host)
	add("port", cfg.Port)
	add("user", cfg.User)
	add("password", cfg.Password)
	add("dbname", cfg.Name)
	add("sslmode", cfg.SSLMode)
	return strings.Join(parts, " ")
}

func quoteDSNValue(v string) string {
	if !strings.ContainsAny(v, " '\\") {
		return v
	}
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `'`, `\'`)
	return "'" + v + "'"
}
