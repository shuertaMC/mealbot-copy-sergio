// Package store contains every SQL statement the service runs against Postgres.
package store

import (
	"context"
	"database/sql"
)

// DBTX is the subset of *sql.DB and *sql.Tx used by the queries in this package.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store runs queries against a shared connection pool.
type Store struct {
	db *sql.DB
}

// New creates a Store on top of db. The caller owns db and closes it.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// inTx runs fn inside a transaction, committing if fn returns nil.
func (s *Store) inTx(ctx context.Context, fn func(q DBTX) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Rollback after a successful Commit is a harmless no-op.
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
