// Package db contains database connection helpers. It is an inlined and
// pgx v5 port of the CreateDBConnection / LocalDBConnection helpers that used
// to live in github.com/johnamadeo/server (see README for the licensing note).
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DuplicateKeyErr : Error message when user tries to insert row with duplicate key
const DuplicateKeyErr = "duplicate key value violates unique constraint"

// Querier is the subset of pgx used by the application. *pgxpool.Pool,
// *pgx.Conn and pgx.Tx all satisfy it, and tests can provide fakes.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// LocalDBConnection describes a local Postgres database used when no
// DATABASE_URL is provided.
type LocalDBConnection struct {
	User   string
	DBName string
}

// DSN returns a keyword/value connection string for the local database.
func (c LocalDBConnection) DSN() string {
	return fmt.Sprintf("user=%s dbname=%s sslmode=disable", c.User, c.DBName)
}

// CreateDBConnection creates a pgx connection pool. When databaseURL is empty
// (i.e. DATABASE_URL is not set, as on a developer machine) the local
// connection settings are used. On Heroku DATABASE_URL is always set:
// https://devcenter.heroku.com/articles/heroku-postgresql#provisioning-heroku-postgres
func CreateDBConnection(ctx context.Context, databaseURL string, local LocalDBConnection) (*pgxpool.Pool, error) {
	dataSource := databaseURL
	if dataSource == "" {
		dataSource = local.DSN()
	}

	pool, err := pgxpool.New(ctx, dataSource)
	if err != nil {
		return nil, err
	}
	return pool, nil
}
