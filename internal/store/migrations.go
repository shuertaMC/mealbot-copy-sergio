package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	migrationassets "github.com/johnamadeo/mealbot/migrations"
)

const (
	defaultMigrationTimeout = time.Minute
	migrationLockKey        = int64(0x4d45414c424f54) // "MEALBOT"
)

var migrationFilename = regexp.MustCompile(`^([0-9]+)_([a-z0-9][a-z0-9_]*)\.sql$`)

const createMigrationLedgerSQL = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT PRIMARY KEY CHECK (version > 0),
    name TEXT NOT NULL CHECK (length(name) > 0),
    checksum TEXT NOT NULL CHECK (length(checksum) = 64),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
)`

// Migration is one immutable, checksummed SQL asset.
type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

// MigrationOptions controls the maximum duration of ledger setup and each
// individual migration. Zero selects a finite production default.
type MigrationOptions struct {
	OperationTimeout time.Duration
}

// Migrator applies a deterministic set of migrations to an injected pool.
type Migrator struct {
	db         *sql.DB
	migrations []Migration
	timeout    time.Duration
}

// LoadMigrations validates and orders all SQL files at the root of fsys.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	if fsys == nil {
		return nil, errors.New("migration filesystem is required")
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	loaded := make([]Migration, 0, len(entries))
	versions := make(map[int64]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		match := migrationFilename.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}
		if previous, exists := versions[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d in %q and %q", version, previous, entry.Name())
		}
		contents, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			return nil, fmt.Errorf("migration %q is empty", entry.Name())
		}
		checksum := sha256.Sum256(contents)
		versions[version] = entry.Name()
		loaded = append(loaded, Migration{
			Version:  version,
			Name:     match[2],
			SQL:      string(contents),
			Checksum: fmt.Sprintf("%x", checksum),
		})
	}
	if len(loaded) == 0 {
		return nil, errors.New("no SQL migrations found")
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].Version < loaded[j].Version })
	return loaded, nil
}

// NewMigrator builds a runner using the checked-in embedded SQL assets.
func NewMigrator(db *sql.DB, options ...MigrationOptions) (*Migrator, error) {
	return NewMigratorFromFS(db, migrationassets.Files, options...)
}

// NewMigratorFromFS builds a runner using an alternate filesystem, primarily
// for validation and focused tests.
func NewMigratorFromFS(db *sql.DB, fsys fs.FS, options ...MigrationOptions) (*Migrator, error) {
	if db == nil {
		return nil, ErrNilDatabase
	}
	loaded, err := LoadMigrations(fsys)
	if err != nil {
		return nil, err
	}
	timeout := defaultMigrationTimeout
	if len(options) > 0 && options[0].OperationTimeout > 0 {
		timeout = options[0].OperationTimeout
	}
	return &Migrator{db: db, migrations: loaded, timeout: timeout}, nil
}

// Migrations returns a copy of the ordered migration metadata.
func (m *Migrator) Migrations() []Migration {
	if m == nil {
		return nil
	}
	result := make([]Migration, len(m.migrations))
	copy(result, m.migrations)
	return result
}

// Apply creates the ledger and applies each outstanding migration in its own
// transaction. An advisory transaction lock serializes competing runners.
func (m *Migrator) Apply(ctx context.Context) error {
	if m == nil || m.db == nil {
		return ErrNilDatabase
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ledgerCtx, cancel := context.WithTimeout(ctx, m.timeout)
	_, err := m.db.ExecContext(ledgerCtx, createMigrationLedgerSQL)
	cancel()
	if err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	if err := m.validateLedger(ctx); err != nil {
		return err
	}
	for _, migration := range m.migrations {
		if err := m.applyOne(ctx, migration); err != nil {
			return err
		}
	}
	return nil
}

func (m *Migrator) validateLedger(ctx context.Context) error {
	queryCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	rows, err := m.db.QueryContext(queryCtx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()

	known := make(map[int64]Migration, len(m.migrations))
	for _, migration := range m.migrations {
		known[migration.Version] = migration
	}
	for rows.Next() {
		var version int64
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return fmt.Errorf("scan migration ledger: %w", err)
		}
		migration, ok := known[version]
		if !ok {
			return fmt.Errorf("database contains unknown migration version %d", version)
		}
		if name != migration.Name || checksum != migration.Checksum {
			return fmt.Errorf("migration %d differs from applied ledger entry", version)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate migration ledger: %w", err)
	}
	return nil
}

func (m *Migrator) applyOne(ctx context.Context, migration Migration) error {
	migrationCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	tx, err := m.db.BeginTx(migrationCtx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", migration.Version, err)
	}
	rollback := func(cause error) error {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return errors.Join(cause, fmt.Errorf("rollback migration %d: %w", migration.Version, rollbackErr))
		}
		return cause
	}

	if _, err := tx.ExecContext(migrationCtx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return rollback(fmt.Errorf("lock migration %d: %w", migration.Version, err))
	}
	var appliedName, appliedChecksum string
	err = tx.QueryRowContext(
		migrationCtx,
		`SELECT name, checksum FROM schema_migrations WHERE version = $1`,
		migration.Version,
	).Scan(&appliedName, &appliedChecksum)
	switch {
	case err == nil:
		if appliedName != migration.Name || appliedChecksum != migration.Checksum {
			return rollback(fmt.Errorf("migration %d differs from applied ledger entry", migration.Version))
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d check: %w", migration.Version, err)
		}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return rollback(fmt.Errorf("check migration %d: %w", migration.Version, err))
	}

	if _, err := tx.ExecContext(migrationCtx, migration.SQL); err != nil {
		return rollback(fmt.Errorf("apply migration %d (%s): %w", migration.Version, migration.Name, err))
	}
	if _, err := tx.ExecContext(
		migrationCtx,
		`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
		migration.Version,
		migration.Name,
		migration.Checksum,
	); err != nil {
		return rollback(fmt.Errorf("record migration %d: %w", migration.Version, err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", migration.Version, err)
	}
	return nil
}
