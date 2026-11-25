package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store is the shared PostgreSQL adapter used by repository implementations.
type Store struct {
	db               *sql.DB
	operationTimeout time.Duration
}

// New constructs a store around an injected database handle. The optional
// duration overrides the finite default used for transactional operations.
func New(db *sql.DB, operationTimeout ...time.Duration) (*Store, error) {
	if db == nil {
		return nil, ErrNilDatabase
	}
	timeout := defaultOperationTimeout
	if len(operationTimeout) > 0 && operationTimeout[0] > 0 {
		timeout = operationTimeout[0]
	}
	return &Store{db: db, operationTimeout: timeout}, nil
}

// DB exposes the injected handle for composition code sharing the pool with
// the migration runner.
func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func (s *Store) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, s.operationTimeout)
}

// WithinTransaction runs fn atomically. Callback and rollback failures are
// both retained, and commit failures are returned to the caller.
func (s *Store) WithinTransaction(ctx context.Context, fn func(DBTX) error) error {
	if s == nil || s.db == nil {
		return ErrNilDatabase
	}
	if fn == nil {
		return errors.New("transaction callback is required")
	}
	txCtx, cancel := s.operationContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback transaction: %w", rollbackErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
