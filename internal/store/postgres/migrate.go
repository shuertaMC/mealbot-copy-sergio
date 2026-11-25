package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/johnamadeo/mealbot/migrations"
)

const migrationLockID int64 = 0x4d45414c424f54

// Migrate applies every embedded forward migration exactly once. Each file and
// its ledger entry execute in one transaction while an advisory transaction
// lock serializes concurrent application instances.
func (r *Repository) Migrate(ctx context.Context) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("postgres migrate: repository is closed")
	}
	if _, err := r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS mealbot_schema_migrations (
			version integer PRIMARY KEY CHECK (version > 0),
			name text NOT NULL UNIQUE,
			applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return mapDatabaseError("create migration ledger", err)
	}

	entries, err := migrationEntries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("postgres begin migration %s: %w", entry.name, err)
		}
		if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
			return rollback(tx, mapDatabaseError("lock migrations", err))
		}
		var applied bool
		if err = tx.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM mealbot_schema_migrations WHERE version = $1)",
			entry.version,
		).Scan(&applied); err != nil {
			return rollback(tx, mapDatabaseError("read migration ledger", err))
		}
		if !applied {
			if _, err = tx.ExecContext(ctx, entry.sql); err != nil {
				return rollback(tx, mapDatabaseError("apply migration "+entry.name, err))
			}
			if _, err = tx.ExecContext(ctx,
				"INSERT INTO mealbot_schema_migrations (version, name) VALUES ($1, $2)",
				entry.version, entry.name,
			); err != nil {
				return rollback(tx, mapDatabaseError("record migration "+entry.name, err))
			}
		}
		if err = tx.Commit(); err != nil {
			return mapDatabaseError("commit migration "+entry.name, err)
		}
	}
	return nil
}

type migrationEntry struct {
	version int
	name    string
	sql     string
}

func migrationEntries() ([]migrationEntry, error) {
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("postgres list migrations: %w", err)
	}
	sort.Strings(names)
	entries := make([]migrationEntry, 0, len(names))
	seen := make(map[int]struct{}, len(names))
	for _, name := range names {
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("postgres migration %q has no numeric prefix", name)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("postgres migration %q has invalid version", name)
		}
		if _, exists := seen[version]; exists {
			return nil, fmt.Errorf("postgres migration version %d is duplicated", version)
		}
		contents, err := migrations.Files.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("postgres read migration %q: %w", name, err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			return nil, fmt.Errorf("postgres migration %q is empty", name)
		}
		seen[version] = struct{}{}
		entries = append(entries, migrationEntry{version: version, name: name, sql: string(contents)})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("postgres: no embedded migrations")
	}
	return entries, nil
}
