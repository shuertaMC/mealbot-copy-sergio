package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const integrationOptIn = "MEALBOT_RUN_POSTGRES_INTEGRATION"

var integrationSchemaSequence atomic.Uint64

func integrationDatabaseURL(t *testing.T) string {
	t.Helper()
	if os.Getenv(integrationOptIn) != "1" {
		t.Skipf("set %s=1 to run PostgreSQL integration tests", integrationOptIn)
	}
	databaseURL := os.Getenv("MEALBOT_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if databaseURL == "" {
		t.Fatal("integration opt-in is set but MEALBOT_TEST_DATABASE_URL and DATABASE_URL are empty")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatalf("integration database must be a postgres:// or postgresql:// URL: %v", err)
	}
	databaseName := strings.ToLower(strings.Trim(parsed.Path, "/"))
	isTestName := databaseName == "test" ||
		strings.HasPrefix(databaseName, "test_") ||
		strings.HasSuffix(databaseName, "_test") ||
		strings.HasSuffix(databaseName, "-test")
	if !isTestName {
		t.Fatalf("refusing integration database %q: use a test/test_*/*_test/*-test database name", databaseName)
	}
	return databaseURL
}

func withIsolatedIntegrationDB(t *testing.T, fn func(context.Context, *sql.DB)) {
	t.Helper()
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer adminDB.Close()

	schema := fmt.Sprintf("mealbot_migration_test_%d_%d", time.Now().UnixNano(), integrationSchemaSequence.Add(1))
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA "%s"`, schema)); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if _, err := adminDB.ExecContext(dropCtx, fmt.Sprintf(`DROP SCHEMA "%s" CASCADE`, schema)); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	}()

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := Open(ctx, parsed.String())
	if err != nil {
		t.Fatalf("open isolated schema connection: %v", err)
	}
	defer db.Close()
	var serverVersion int
	if err := db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&serverVersion); err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}
	if serverVersion/10000 != 19 {
		t.Fatalf("PostgreSQL major version = %d, want 19", serverVersion/10000)
	}
	fn(ctx, db)
}

func TestMigrationsPostgreSQLStartingShapes(t *testing.T) {
	shapes := []struct {
		name          string
		memberColumns string
		history       string
		wantLegacy    string
		wantCurrent   string
	}{
		{name: "empty"},
		{
			name:          "pair_counts",
			memberColumns: `pair_counts JSONB NOT NULL`,
			history:       `'{"legacy@example.com": 4}'::jsonb`,
			wantLegacy:    "4",
		},
		{
			name:          "last_round_with",
			memberColumns: `last_round_with JSONB NOT NULL`,
			history:       `'{"current@example.com": 7}'::jsonb`,
			wantCurrent:   "7",
		},
		{
			name:          "both_history_columns",
			memberColumns: `pair_counts JSONB NOT NULL, last_round_with JSONB NOT NULL`,
			history:       `'{"legacy@example.com": 4}'::jsonb, '{"current@example.com": 7}'::jsonb`,
			wantLegacy:    "4",
			wantCurrent:   "7",
		},
	}
	for _, shape := range shapes {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			withIsolatedIntegrationDB(t, func(ctx context.Context, db *sql.DB) {
				if shape.memberColumns != "" {
					if _, err := db.ExecContext(ctx, `
CREATE TABLE organizations (
    name VARCHAR PRIMARY KEY,
    admin VARCHAR NOT NULL CHECK (length(admin) > 0),
    cross_match_trait VARCHAR
);
INSERT INTO organizations (name, admin) VALUES ('test-org', 'admin@example.com');
CREATE TABLE members (
    organization VARCHAR NOT NULL REFERENCES organizations(name),
    email VARCHAR NOT NULL,
    name VARCHAR NOT NULL,
    metadata JSONB,
    `+shape.memberColumns+`,
    active BOOLEAN NOT NULL,
    PRIMARY KEY (organization, email)
);
INSERT INTO members (organization, email, name, metadata, `+historyColumnList(shape.name)+`, active)
VALUES ('test-org', 'member@example.com', 'Member', '{}'::jsonb, `+shape.history+`, TRUE);
`); err != nil {
						t.Fatalf("create %s starting shape: %v", shape.name, err)
					}
				}

				migrator, err := NewMigrator(db)
				if err != nil {
					t.Fatal(err)
				}
				if err := migrator.Apply(ctx); err != nil {
					t.Fatalf("first Apply(): %v", err)
				}
				if err := migrator.Apply(ctx); err != nil {
					t.Fatalf("second Apply(): %v", err)
				}

				var ledgerCount int
				if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&ledgerCount); err != nil {
					t.Fatal(err)
				}
				if ledgerCount != 3 {
					t.Fatalf("ledger count = %d, want 3", ledgerCount)
				}
				var organizationsExists, membersExists, roundsExists, pairsExists bool
				if err := db.QueryRowContext(ctx, `
SELECT to_regclass('organizations') IS NOT NULL,
       to_regclass('members') IS NOT NULL,
       to_regclass('rounds') IS NOT NULL,
       to_regclass('pairs') IS NOT NULL
`).Scan(&organizationsExists, &membersExists, &roundsExists, &pairsExists); err != nil {
					t.Fatal(err)
				}
				if !organizationsExists || !membersExists || !roundsExists || !pairsExists {
					t.Fatalf("missing schema table: organizations=%t members=%t rounds=%t pairs=%t", organizationsExists, membersExists, roundsExists, pairsExists)
				}
				var lastRoundColumns, pairCountColumns int
				if err := db.QueryRowContext(ctx, `
SELECT
    count(*) FILTER (WHERE column_name = 'last_round_with'),
    count(*) FILTER (WHERE column_name = 'pair_counts')
FROM information_schema.columns
WHERE table_schema = current_schema() AND table_name = 'members'
`).Scan(&lastRoundColumns, &pairCountColumns); err != nil {
					t.Fatal(err)
				}
				if lastRoundColumns != 1 || pairCountColumns != 0 {
					t.Fatalf("history columns: last_round_with=%d pair_counts=%d", lastRoundColumns, pairCountColumns)
				}
				if shape.memberColumns != "" {
					var legacy, current sql.NullString
					if err := db.QueryRowContext(ctx, `
SELECT last_round_with ->> 'legacy@example.com',
       last_round_with ->> 'current@example.com'
FROM members WHERE email = 'member@example.com'
`).Scan(&legacy, &current); err != nil {
						t.Fatal(err)
					}
					if legacy.String != shape.wantLegacy || current.String != shape.wantCurrent {
						t.Fatalf("history values = legacy:%q current:%q", legacy.String, current.String)
					}
				}
			})
		})
	}
}

func historyColumnList(shape string) string {
	if shape == "both_history_columns" {
		return "pair_counts, last_round_with"
	}
	return shape
}
