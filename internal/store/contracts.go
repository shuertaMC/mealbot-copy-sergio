// Package store owns PostgreSQL-backed persistence and transactional boundaries.
package store

import (
	"context"
	"database/sql"
)

// DBTX is the shared query surface implemented by both *sql.DB and *sql.Tx.
// Repository methods can therefore participate in caller-owned transactions.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Transactor executes a callback atomically.
type Transactor interface {
	WithinTransaction(context.Context, func(DBTX) error) error
}
