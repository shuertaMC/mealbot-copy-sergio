// Package testutil provides a real Postgres (in a testcontainers container)
// for integration tests.
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/johnamadeo/mealbot"
	"github.com/johnamadeo/mealbot/internal/server"
)

const (
	postgresImage = "postgres:16-alpine"
	// RequireDockerEnv makes tests fail instead of skip when Docker is unavailable.
	RequireDockerEnv = "MEALBOT_REQUIRE_DOCKER"
)

var (
	startOnce sync.Once
	startDSN  string
	startErr  error
)

func startPostgres() {
	defer func() {
		if r := recover(); r != nil {
			startErr = fmt.Errorf("starting postgres container panicked: %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	container, err := postgres.Run(
		ctx,
		postgresImage,
		postgres.WithDatabase("mealbot"),
		postgres.WithUsername("mealbot"),
		postgres.WithPassword("mealbot"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		startErr = err
		return
	}

	// The container lives until the test binary exits, where testcontainers'
	// reaper removes it.
	startDSN, startErr = container.ConnectionString(ctx, "sslmode=disable")
}

// NewDB returns a connection to a Postgres database whose tables were freshly
// created from schema.sql. The test is skipped when Docker is not available
// (or failed when MEALBOT_REQUIRE_DOCKER is set).
func NewDB(t *testing.T) *sql.DB {
	t.Helper()

	required := os.Getenv(RequireDockerEnv) != ""
	if !required {
		testcontainers.SkipIfProviderIsNotHealthy(t)
	}

	startOnce.Do(startPostgres)
	if startErr != nil {
		if required {
			t.Fatalf("postgres container unavailable: %v", startErr)
		}
		t.Skipf("postgres container unavailable: %v", startErr)
	}

	db, err := server.CreateDBConnection(startDSN)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := db.ExecContext(ctx, mealbot.Schema); err != nil {
		t.Fatalf("load schema.sql: %v", err)
	}

	return db
}
