package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/domain"
)

const driverName = "pgx"

// Repository is a concurrency-safe PostgreSQL adapter. It owns no mutable
// process globals; database/sql manages its connection pool.
type Repository struct {
	db *sql.DB
}

// Open constructs and verifies a PostgreSQL connection pool from a validated
// database selection. A failed ping closes the partially opened pool.
func Open(ctx context.Context, database config.Database) (*Repository, error) {
	return openWith(ctx, database, sql.Open)
}

func openWith(ctx context.Context, database config.Database, opener func(string, string) (*sql.DB, error)) (*Repository, error) {
	dsn, err := connectionString(database)
	if err != nil {
		return nil, err
	}
	db, err := opener(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("postgres ping: %w (close: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return &Repository{db: db}, nil
}

// New wraps an existing database/sql pool. It is intended for composition and
// tests that already own pool configuration; Open is preferred at startup.
func New(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("postgres: database pool is nil")
	}
	return &Repository{db: db}, nil
}

// Ping verifies that the repository can reach PostgreSQL.
func (r *Repository) Ping(ctx context.Context) error {
	if r == nil || r.db == nil {
		return errors.New("postgres ping: repository is closed")
	}
	if err := r.db.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	return nil
}

// Close releases the database pool. It is safe to call once after successful
// construction; database/sql defines repeated Close calls as harmless.
func (r *Repository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	if err := r.db.Close(); err != nil {
		return fmt.Errorf("postgres close: %w", err)
	}
	return nil
}

func connectionString(database config.Database) (string, error) {
	if database.URL != "" {
		parsed, err := url.Parse(database.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
			return "", domain.NewValidationError("database", "must be a PostgreSQL URL")
		}
		return database.URL, nil
	}
	if strings.TrimSpace(database.User) == "" || strings.TrimSpace(database.Name) == "" {
		return "", domain.NewValidationError("database", "user and name are required")
	}
	local := &url.URL{
		Scheme:   "postgresql",
		User:     url.User(database.User),
		Host:     "localhost",
		Path:     "/" + database.Name,
		RawQuery: "sslmode=disable",
	}
	return local.String(), nil
}

func mapDatabaseError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NewNotFoundError(operation, "")
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "23505", "23P01":
			return domain.NewConflictError(operation, "conflicts with existing state", err)
		case "23502", "23503", "23514", "22023":
			return domain.NewIntegrityError(operation, "database constraint failed", err)
		}
	}
	return fmt.Errorf("postgres %s: %w", operation, err)
}

func rollback(tx *sql.Tx, cause error) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("%w (rollback: %v)", cause, err)
	}
	return cause
}
