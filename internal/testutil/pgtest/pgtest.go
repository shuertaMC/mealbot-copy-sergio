// Package pgtest starts a throwaway Postgres container (testcontainers-go)
// initialised with the repository's schema.sql for integration tests. Tests
// are skipped cleanly when Docker is not available.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const image = "postgres:16-alpine"

// NewPool returns a pool connected to a fresh Postgres container with
// schema.sql applied. It calls t.Skip when the container cannot be started
// (e.g. no Docker daemon).
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	// Some testcontainers code paths panic when Docker is missing.
	var pool *pgxpool.Pool
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Skipf("skipping integration test, Docker unavailable: %v", r)
			}
		}()
		pool = newPool(t)
	}()
	return pool
}

func newPool(t *testing.T) *pgxpool.Pool {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	schema, err := schemaPath()
	if err != nil {
		t.Fatalf("locating schema.sql: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Random password for the ephemeral container.
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generating password: %v", err)
	}
	password := hex.EncodeToString(buf)

	ctr, err := postgres.Run(
		ctx,
		image,
		postgres.WithDatabase("mealbot"),
		postgres.WithUsername("mealbot"),
		postgres.WithPassword(password),
		postgres.WithInitScripts(schema),
		postgres.BasicWaitStrategies(),
	)
	if ctr != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })
	}
	if err != nil {
		t.Skipf("skipping integration test, cannot start Postgres container: %v", err)
	}

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("creating pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pinging database: %v", err)
	}
	return pool
}

// schemaPath walks up from the working directory to the module root and
// returns the path of schema.sql.
func schemaPath() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "schema.sql"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above working directory")
		}
		dir = parent
	}
}
