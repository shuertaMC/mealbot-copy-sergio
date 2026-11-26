// Package postgrestest provides shared safety guards for PostgreSQL test suites.
package postgrestest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode"
)

// suiteLockKey is the ASCII string "MEALBOTT" encoded as an int64. Every
// destructive PostgreSQL integration suite uses this stable session advisory
// lock so package test binaries can safely share DATABASE_URL.
const suiteLockKey int64 = 0x4d45414c424f5454

// ValidateDatabaseURL returns the selected database name when raw is an
// absolute PostgreSQL URL whose path contains "test" as a delimited segment.
// It rejects query-string database overrides so destructive test setup cannot
// target a different database than the validated path.
func ValidateDatabaseURL(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("DATABASE_URL must be a non-empty PostgreSQL URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("DATABASE_URL must be a valid PostgreSQL URL: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "postgres") && !strings.EqualFold(parsed.Scheme, "postgresql") {
		return "", fmt.Errorf("DATABASE_URL must use the postgres or postgresql scheme")
	}
	if parsed.Opaque != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("DATABASE_URL must be an absolute PostgreSQL URL without a fragment")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", fmt.Errorf("DATABASE_URL has an invalid query: %w", err)
	}
	for key := range query {
		if strings.EqualFold(key, "dbname") {
			return "", fmt.Errorf("DATABASE_URL must select its database in the URL path, not a dbname query parameter")
		}
	}
	if !strings.HasPrefix(parsed.Path, "/") || len(parsed.Path) == 1 {
		return "", fmt.Errorf("DATABASE_URL must select a database in its URL path")
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if databaseName == "" || strings.ContainsRune(databaseName, '\x00') {
		return "", fmt.Errorf("DATABASE_URL must select a valid database name")
	}
	if !hasDelimitedTestSegment(databaseName) {
		return "", fmt.Errorf(
			"refusing destructive integration test against database %q: name must contain test as a delimited segment",
			databaseName,
		)
	}
	return databaseName, nil
}

// AcquireSuiteLock acquires the shared PostgreSQL test advisory lock on a
// dedicated connection. The lock remains held until test cleanup, which
// verifies its release before closing the dedicated connection.
func AcquireSuiteLock(t testing.TB, ctx context.Context, db *sql.DB) {
	t.Helper()
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	connection, err := db.Conn(lockContext)
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL integration lock session: %v", err)
	}
	if _, err := connection.ExecContext(lockContext, "SELECT pg_advisory_lock($1)", suiteLockKey); err != nil {
		closeErr := connection.Close()
		t.Fatalf("acquire PostgreSQL integration advisory lock: %v; close lock session: %v", err, closeErr)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		var released bool
		if err := connection.QueryRowContext(cleanupContext,
			"SELECT pg_advisory_unlock($1)", suiteLockKey,
		).Scan(&released); err != nil {
			t.Errorf("release PostgreSQL integration advisory lock: %v", err)
		} else if !released {
			t.Error("release PostgreSQL integration advisory lock: lock was not held by dedicated session")
		}
		if err := connection.Close(); err != nil {
			t.Errorf("close PostgreSQL integration lock session: %v", err)
		}
	})
}

func hasDelimitedTestSegment(databaseName string) bool {
	segmentStart := -1
	for index, character := range databaseName {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			if segmentStart == -1 {
				segmentStart = index
			}
			continue
		}
		if segmentStart != -1 && strings.EqualFold(databaseName[segmentStart:index], "test") {
			return true
		}
		segmentStart = -1
	}
	return segmentStart != -1 && strings.EqualFold(databaseName[segmentStart:], "test")
}
