package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/domain"
)

func TestConnectionString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		database config.Database
		contains string
		wantErr  bool
	}{
		{name: "hosted", database: config.Database{URL: "postgresql://db.example/mealbot?sslmode=require"}, contains: "db.example/mealbot"},
		{name: "local escaped", database: config.Database{User: "meal bot", Name: "mealbot-test"}, contains: "meal%20bot@localhost/mealbot-test?sslmode=disable"},
		{name: "missing", database: config.Database{}, wantErr: true},
		{name: "wrong scheme", database: config.Database{URL: "mysql://db.example/mealbot"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := connectionString(test.database)
			if test.wantErr {
				if !domain.IsValidation(err) {
					t.Fatalf("connectionString error = %v, want validation", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("connectionString() error = %v", err)
			}
			if !strings.Contains(got, test.contains) {
				t.Fatalf("connectionString() = %q, want substring %q", got, test.contains)
			}
		})
	}
}

func TestMapDatabaseError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		code string
		kind domain.ErrorKind
	}{
		{name: "unique", code: "23505", kind: domain.ErrorKindConflict},
		{name: "exclusion", code: "23P01", kind: domain.ErrorKindConflict},
		{name: "foreign key", code: "23503", kind: domain.ErrorKindIntegrity},
		{name: "check", code: "23514", kind: domain.ErrorKindIntegrity},
		{name: "unknown", code: "XX000", kind: domain.ErrorKindUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cause := &pgconn.PgError{Code: test.code, Message: "private database detail"}
			got := mapDatabaseError("write", cause)
			if kind := domain.KindOf(got); kind != test.kind {
				t.Fatalf("KindOf(error) = %q, want %q (%v)", kind, test.kind, got)
			}
			if !errors.Is(got, cause) {
				t.Fatalf("mapped error does not wrap cause: %v", got)
			}
		})
	}
	if got := mapDatabaseError("noop", nil); got != nil {
		t.Fatalf("nil error mapped to %v", got)
	}
}

func TestJSONAndHistoryHelpers(t *testing.T) {
	t.Parallel()
	metadata, err := decodeStringMap([]byte(`{"team":"blue"}`))
	if err != nil || !reflect.DeepEqual(metadata, map[string]string{"team": "blue"}) {
		t.Fatalf("decodeStringMap = %#v, %v", metadata, err)
	}
	if _, err := decodeStringMap([]byte(`{"team":3}`)); err == nil {
		t.Fatal("decodeStringMap accepted a non-string value")
	}
	history, err := decodeHistory([]byte(`{"b@example.test":4}`))
	if err != nil || history["b@example.test"] != 4 {
		t.Fatalf("decodeHistory = %#v, %v", history, err)
	}
	if empty, err := decodeStringMap(nil); err != nil || len(empty) != 0 {
		t.Fatalf("decodeStringMap(nil) = %#v, %v", empty, err)
	}
	if empty, err := decodeHistory([]byte("null")); err != nil || len(empty) != 0 {
		t.Fatalf("decodeHistory(null) = %#v, %v", empty, err)
	}
	if encoded, err := encodeStringMap(nil); err != nil || string(encoded) != "{}" {
		t.Fatalf("encodeStringMap(nil) = %s, %v", encoded, err)
	}
	if encoded, err := encodeHistory(nil); err != nil || string(encoded) != "{}" {
		t.Fatalf("encodeHistory(nil) = %s, %v", encoded, err)
	}
	if _, err := decodeHistory([]byte(`{"b@example.test":"four"}`)); err == nil {
		t.Fatal("decodeHistory accepted a non-integer value")
	}
	merged := mergeHistory(map[string]int{"a": 2, "b": 5}, map[string]int{"a": 4, "b": 3, "c": -1})
	if want := map[string]int{"a": 4, "b": 5, "c": -1}; !reflect.DeepEqual(merged, want) {
		t.Fatalf("mergeHistory = %#v, want %#v", merged, want)
	}
}

func TestSmallRepositoryHelpers(t *testing.T) {
	t.Parallel()
	if !domain.IsNotFound(mapDatabaseError("lookup", sql.ErrNoRows)) {
		t.Fatal("sql.ErrNoRows was not mapped to not-found")
	}
	if nullableExtraID("") != nil || nullableExtraID("a@example.test") != "a@example.test" {
		t.Fatal("nullableExtraID contract failed")
	}
	if err := (*Repository)(nil).Migrate(context.Background()); err == nil {
		t.Fatal("nil repository Migrate succeeded")
	}
}

func TestMakeHistoriesSymmetric(t *testing.T) {
	t.Parallel()
	members := map[string]domain.Member{
		"a@example.test": {Email: "a@example.test", LastRoundWith: map[string]int{"b@example.test": 2, "a@example.test": 8}},
		"b@example.test": {Email: "b@example.test", LastRoundWith: map[string]int{"a@example.test": 5}},
		"c@example.test": {Email: "c@example.test"},
	}
	makeHistoriesSymmetric(members)
	if members["a@example.test"].LastRoundWith["b@example.test"] != 5 ||
		members["b@example.test"].LastRoundWith["a@example.test"] != 5 {
		t.Fatalf("asymmetric latest history: %#v", members)
	}
	if members["a@example.test"].LastRoundWith["c@example.test"] != -1 ||
		members["c@example.test"].LastRoundWith["a@example.test"] != -1 {
		t.Fatalf("new relationship not initialized symmetrically: %#v", members)
	}
	if _, self := members["a@example.test"].LastRoundWith["a@example.test"]; self {
		t.Fatalf("self history was retained: %#v", members)
	}
}

func TestUpdatePairHistory(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pair    domain.Pair
		roundID int
		want    map[string]map[string]int
	}{
		{
			name:    "pair",
			pair:    domain.Pair{ID1: "a@example.test", ID2: "b@example.test"},
			roundID: 4,
			want: map[string]map[string]int{
				"a@example.test": {"b@example.test": 4, "legacy@example.test": 1},
				"b@example.test": {"a@example.test": 4, "legacy@example.test": 2},
				"c@example.test": {"d@example.test": 3},
				"d@example.test": {"c@example.test": 3},
			},
		},
		{
			name: "triple",
			pair: domain.Pair{
				ID1:     "a@example.test",
				ID2:     "b@example.test",
				ExtraID: "c@example.test",
			},
			roundID: 7,
			want: map[string]map[string]int{
				"a@example.test": {"b@example.test": 7, "c@example.test": 7, "legacy@example.test": 1},
				"b@example.test": {"a@example.test": 7, "c@example.test": 7, "legacy@example.test": 2},
				"c@example.test": {"a@example.test": 7, "b@example.test": 7, "d@example.test": 3},
				"d@example.test": {"c@example.test": 3},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			members := map[string]domain.Member{
				"a@example.test": {Email: "a@example.test", LastRoundWith: map[string]int{"legacy@example.test": 1}},
				"b@example.test": {Email: "b@example.test", LastRoundWith: map[string]int{"legacy@example.test": 2}},
				"c@example.test": {Email: "c@example.test", LastRoundWith: map[string]int{"d@example.test": 3}},
				"d@example.test": {Email: "d@example.test", LastRoundWith: map[string]int{"c@example.test": 3}},
			}

			updatePairHistory(members, test.pair, test.roundID)

			for email, member := range members {
				if !reflect.DeepEqual(member.LastRoundWith, test.want[email]) {
					t.Errorf("%s history = %#v, want %#v", email, member.LastRoundWith, test.want[email])
				}
				if _, self := member.LastRoundWith[email]; self {
					t.Errorf("%s history contains a self edge: %#v", email, member.LastRoundWith)
				}
			}
		})
	}
}

func TestMigrationEntries(t *testing.T) {
	t.Parallel()
	entries, err := migrationEntries()
	if err != nil {
		t.Fatalf("migrationEntries() error = %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("len(entries) = %d, want 4", len(entries))
	}
	for index, entry := range entries {
		if entry.version != index+1 {
			t.Fatalf("entry %d version = %d", index, entry.version)
		}
		if strings.TrimSpace(entry.sql) == "" {
			t.Fatalf("entry %d has empty SQL", index)
		}
	}
}

func TestSchemaBootstrapIsFailFastAndTransactional(t *testing.T) {
	t.Parallel()
	schema, err := os.ReadFile(filepath.Join("..", "..", "..", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	markers := []string{
		`\set ON_ERROR_STOP on`,
		"BEGIN;",
		`\ir migrations/001_initial_schema.sql`,
		`\ir migrations/002_reconcile_legacy_schema.sql`,
		`\ir migrations/003_constraints_and_indexes.sql`,
		`\ir migrations/004_stable_round_claims.sql`,
		"COMMIT;",
	}
	position := -1
	for _, marker := range markers {
		next := strings.Index(string(schema), marker)
		if next <= position {
			t.Fatalf("schema bootstrap marker %q is missing or out of order", marker)
		}
		position = next
	}
}

func TestRepositoryNilGuards(t *testing.T) {
	t.Parallel()
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) succeeded")
	}
	var repository *Repository
	if err := repository.Close(); err != nil {
		t.Fatalf("nil Close() = %v", err)
	}
}
