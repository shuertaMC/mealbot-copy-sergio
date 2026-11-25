package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	migrationassets "github.com/johnamadeo/mealbot/migrations"
)

func TestLoadMigrationsOrdersNumericallyAndChecksumsContents(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"010_tenth.sql":  {Data: []byte("SELECT 10;")},
		"002_second.sql": {Data: []byte("SELECT 2;")},
	}
	migrations, err := LoadMigrations(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 || migrations[0].Version != 2 || migrations[1].Version != 10 {
		t.Fatalf("migration order = %+v", migrations)
	}
	if len(migrations[0].Checksum) != 64 || migrations[0].Checksum == migrations[1].Checksum {
		t.Fatalf("invalid checksums: %+v", migrations)
	}
}

func TestLoadMigrationsRejectsDuplicateVersionsAndMalformedAssets(t *testing.T) {
	t.Parallel()
	tests := map[string]fstest.MapFS{
		"duplicate version": {
			"001_first.sql": {Data: []byte("SELECT 1;")},
			"1_again.sql":   {Data: []byte("SELECT 2;")},
		},
		"bad filename": {
			"migration.sql": {Data: []byte("SELECT 1;")},
		},
		"empty migration": {
			"001_empty.sql": {Data: []byte("  \n")},
		},
	}
	for name, files := range tests {
		name, files := name, files
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadMigrations(files); err == nil {
				t.Fatal("LoadMigrations() succeeded, want validation error")
			}
		})
	}
}

func TestCheckedInMigrationAssetsDefineCompatibleNonDestructiveSchema(t *testing.T) {
	t.Parallel()
	migrations, err := LoadMigrations(migrationassets.Files)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 3 {
		t.Fatalf("migration count = %d, want 3", len(migrations))
	}
	combined := strings.ToLower(migrations[0].SQL + migrations[1].SQL + migrations[2].SQL)
	for _, required := range []string{
		"create table if not exists organizations",
		"create table if not exists members",
		"create table if not exists rounds",
		"create table if not exists pairs",
		"pair_counts",
		"last_round_with",
		"rename column pair_counts",
	} {
		if !strings.Contains(combined, required) {
			t.Errorf("migration assets do not contain %q", required)
		}
	}
	if strings.Contains(combined, "drop table") {
		t.Error("migration assets contain destructive DROP TABLE")
	}
}

func TestMigratorAppliesOutstandingMigrationsInVersionOrder(t *testing.T) {
	state := &driverState{}
	db := openDriverDB(state)
	defer db.Close()
	migrator, err := NewMigratorFromFS(db, fstest.MapFS{
		"010_tenth.sql":  {Data: []byte("SELECT 10;")},
		"002_second.sql": {Data: []byte("SELECT 2;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := strings.Join(state.snapshot(), "\n")
	second := strings.Index(events, "exec:SELECT 2;")
	tenth := strings.Index(events, "exec:SELECT 10;")
	if second < 0 || tenth < 0 || second >= tenth {
		t.Fatalf("migration execution order was not 2 then 10:\n%s", events)
	}
}

func TestMigratorRollsBackFailedMigration(t *testing.T) {
	state := &driverState{failSQL: "BROKEN"}
	db := openDriverDB(state)
	defer db.Close()
	migrator, err := NewMigratorFromFS(db, fstest.MapFS{
		"001_broken.sql": {Data: []byte("BROKEN;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = migrator.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "apply migration 1") {
		t.Fatalf("Apply() error = %v", err)
	}
	if got := state.snapshot(); len(got) == 0 || got[len(got)-1] != "rollback" {
		t.Fatalf("events = %v, want final rollback", got)
	}
	if len(state.applied) != 0 {
		t.Fatalf("failed migration was recorded: %+v", state.applied)
	}
}

func TestMigratorIsIdempotent(t *testing.T) {
	state := &driverState{}
	db := openDriverDB(state)
	defer db.Close()
	migrator, err := NewMigratorFromFS(db, fstest.MapFS{
		"001_once.sql": {Data: []byte("SELECT 'apply once';")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range state.snapshot() {
		if event == "exec:SELECT 'apply once';" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("migration SQL executed %d times, want once", count)
	}
}

func TestMigratorRejectsChecksumOrVersionDrift(t *testing.T) {
	files := fstest.MapFS{"001_first.sql": {Data: []byte("SELECT 1;")}}
	migrations, err := LoadMigrations(files)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]map[int64]ledgerEntry{
		"checksum changed": {
			1: {name: migrations[0].Name, checksum: strings.Repeat("0", 64)},
		},
		"unknown version": {
			2: {name: "future", checksum: strings.Repeat("1", 64)},
		},
	}
	for name, applied := range tests {
		name, applied := name, applied
		t.Run(name, func(t *testing.T) {
			state := &driverState{applied: applied}
			db := openDriverDB(state)
			defer db.Close()
			migrator, err := NewMigratorFromFS(db, files)
			if err != nil {
				t.Fatal(err)
			}
			if err := migrator.Apply(context.Background()); err == nil {
				t.Fatal("Apply() succeeded, want ledger validation error")
			}
		})
	}
}

func TestMigratorPropagatesRollbackError(t *testing.T) {
	rollbackErr := errors.New("rollback failed")
	state := &driverState{failSQL: "BROKEN", rollbackErr: rollbackErr}
	db := openDriverDB(state)
	defer db.Close()
	migrator, err := NewMigratorFromFS(db, fstest.MapFS{
		"001_broken.sql": {Data: []byte("BROKEN;")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Apply(context.Background()); !errors.Is(err, rollbackErr) {
		t.Fatalf("Apply() error = %v, want rollback error", err)
	}
}
