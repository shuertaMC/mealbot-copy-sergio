package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/johnamadeo/mealbot/internal/domain"
)

//go:embed testdata/reset.sql
var resetSQL string

// postgresIntegrationSuiteLockKey is the ASCII string "MEALBOTT" encoded as
// an int64. Every destructive PostgreSQL integration suite uses this stable
// session advisory lock so package test binaries can safely share DATABASE_URL.
const postgresIntegrationSuiteLockKey int64 = 0x4d45414c424f5454

func TestPostgresRepositoryIntegration(t *testing.T) {
	if os.Getenv("RUN_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set RUN_POSTGRES_INTEGRATION=1 with a disposable DATABASE_URL")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if _, err := validatedTestDatabaseName(databaseURL); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := sql.Open(driverName, databaseURL)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close integration database: %v", err)
		}
	})
	acquirePostgresIntegrationSuiteLock(t, ctx, db)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("database ping: %v", err)
	}
	if _, err := db.ExecContext(ctx, resetSQL); err != nil {
		t.Fatalf("reset database: %v", err)
	}
	repository, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := repository.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := repository.Migrate(ctx); err != nil {
		t.Fatalf("idempotent Migrate: %v", err)
	}

	trait := "team"
	organization, _ := domain.NewOrganization("integration-org", "admin@example.test", nil)
	if err := repository.CreateOrganization(ctx, organization); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	if err := repository.CreateOrganization(ctx, organization); !domain.IsConflict(err) {
		t.Fatalf("duplicate organization error = %v, want conflict", err)
	}
	if err := repository.UpdateCrossMatchTrait(ctx, organization.Name, &trait); err != nil {
		t.Fatalf("UpdateCrossMatchTrait: %v", err)
	}
	gotTrait, err := repository.CrossMatchTrait(ctx, organization.Name)
	if err != nil || gotTrait == nil || *gotTrait != trait {
		t.Fatalf("CrossMatchTrait = %v, %v", gotTrait, err)
	}
	organizations, err := repository.ListOrganizations(ctx, organization.Admin)
	if err != nil || len(organizations) != 1 {
		t.Fatalf("ListOrganizations = %#v, %v", organizations, err)
	}

	memberA := integrationMember(t, organization.Name, "a@example.test", "Alpha", map[string]string{"team": "red"})
	memberB := integrationMember(t, organization.Name, "b@example.test", "Beta", map[string]string{"team": "blue"})
	memberC := integrationMember(t, organization.Name, "c@example.test", "Gamma", map[string]string{"team": "green"})
	active, err := repository.ReconcileMembers(ctx, organization.Name, []domain.Member{memberA, memberB, memberC})
	if err != nil || len(active) != 3 {
		t.Fatalf("initial ReconcileMembers = %#v, %v", active, err)
	}
	memberA.Name = "Alpha Updated"
	active, err = repository.ReconcileMembers(ctx, organization.Name, []domain.Member{memberA, memberC})
	if err != nil || len(active) != 2 {
		t.Fatalf("deactivate ReconcileMembers = %#v, %v", active, err)
	}
	allMembers, err := repository.ListMembers(ctx, organization.Name, false)
	if err != nil || len(allMembers) != 3 {
		t.Fatalf("historical ListMembers = %#v, %v", allMembers, err)
	}
	assertMemberState(t, allMembers, "a@example.test", true, "Alpha Updated")
	assertMemberState(t, allMembers, "b@example.test", false, "Beta")
	active, err = repository.ReconcileMembers(ctx, organization.Name, []domain.Member{memberA, memberB, memberC})
	if err != nil || len(active) != 3 {
		t.Fatalf("reactivate ReconcileMembers = %#v, %v", active, err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE FUNCTION mealbot_test_fail_member_reconcile() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.email = 'b@example.test' THEN
				RAISE EXCEPTION 'injected member reconciliation failure';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER mealbot_test_fail_member_reconcile BEFORE INSERT OR UPDATE ON members
		FOR EACH ROW EXECUTE FUNCTION mealbot_test_fail_member_reconcile()`); err != nil {
		t.Fatalf("create member reconciliation failure trigger: %v", err)
	}
	failedMemberA := memberA.Clone()
	failedMemberA.Name = "Alpha Transaction Attempt"
	failedActive, err := repository.ReconcileMembers(ctx, organization.Name, []domain.Member{failedMemberA, memberB, memberC})
	if err == nil || failedActive != nil {
		t.Fatalf("ReconcileMembers injected failure = %#v, %v", failedActive, err)
	}
	if _, err := db.ExecContext(ctx, `
		DROP TRIGGER mealbot_test_fail_member_reconcile ON members;
		DROP FUNCTION mealbot_test_fail_member_reconcile()`); err != nil {
		t.Fatalf("drop member reconciliation failure trigger: %v", err)
	}
	rolledBackMembers, err := repository.ListMembers(ctx, organization.Name, false)
	if err != nil {
		t.Fatalf("read member reconciliation rollback state: %v", err)
	}
	assertMemberState(t, rolledBackMembers, memberA.Email, true, "Alpha Updated")
	assertMemberState(t, rolledBackMembers, memberB.Email, true, "Beta")

	base := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	round0, err := repository.AddRound(ctx, organization.Name, base)
	if err != nil || round0.ID != 0 {
		t.Fatalf("AddRound zero = %#v, %v", round0, err)
	}
	round1, _ := repository.AddRound(ctx, organization.Name, base.Add(time.Hour))
	round2, _ := repository.AddRound(ctx, organization.Name, base.Add(2*time.Hour))
	round3, _ := repository.AddRound(ctx, organization.Name, base.Add(3*time.Hour))
	newTime := base.Add(90 * time.Minute)
	if err := repository.RescheduleRound(ctx, organization.Name, round1.ID, newTime); err != nil {
		t.Fatalf("RescheduleRound: %v", err)
	}
	pairAB, _ := domain.NewPair(memberA.Email, memberB.Email)
	triple, _ := domain.NewPair(memberA.Email, memberB.Email, memberC.Email)
	if err := repository.PersistPairings(ctx, organization.Name, round0.ID, nil); err != nil {
		t.Fatalf("PersistPairings empty: %v", err)
	}
	if err := repository.PersistPairings(ctx, organization.Name, round2.ID, []domain.Pair{pairAB}); err != nil {
		t.Fatalf("PersistPairings pair: %v", err)
	}
	if err := repository.PersistPairings(ctx, organization.Name, round3.ID, []domain.Pair{triple}); err != nil {
		t.Fatalf("PersistPairings triple: %v", err)
	}
	if err := repository.DeleteRound(ctx, organization.Name, round1.ID); err != nil {
		t.Fatalf("DeleteRound: %v", err)
	}
	rounds, err := repository.ListRounds(ctx, organization.Name)
	if err != nil || len(rounds) != 3 || rounds[0].ID != 0 || rounds[1].ID != 1 || rounds[2].ID != 2 {
		t.Fatalf("compacted ListRounds = %#v, %v", rounds, err)
	}
	pairRounds, err := repository.ListPairs(ctx, organization.Name)
	if err != nil || len(pairRounds) != 3 || pairRounds[0].ID != 0 || len(pairRounds[0].Pairs) != 0 ||
		pairRounds[1].ID != 1 || len(pairRounds[1].Pairs) != 1 || pairRounds[2].ID != 2 || len(pairRounds[2].Pairs) != 1 {
		t.Fatalf("compacted ListPairs = %#v, %v", pairRounds, err)
	}
	allMembers, _ = repository.ListMembers(ctx, organization.Name, false)
	assertSymmetricRound(t, allMembers, memberA.Email, memberB.Email, 2)
	assertSymmetricRound(t, allMembers, memberA.Email, memberC.Email, 2)

	if _, err := repository.ReconcileMembers(ctx, organization.Name, []domain.Member{memberA, memberC}); err != nil {
		t.Fatalf("deactivate historical member: %v", err)
	}
	historical, err := repository.ListHistoricalPairs(ctx, organization.Name)
	if err != nil || len(historical) != 3 || len(historical[0].Pairs) != 0 || historical[1].Pairs[0].Member2.Active {
		t.Fatalf("ListHistoricalPairs = %#v, %v", historical, err)
	}
	if err := repository.ReconstructOrganizationHistory(ctx, organization.Name); err != nil {
		t.Fatalf("ReconstructOrganizationHistory: %v", err)
	}
	if err := repository.ReconstructHistory(ctx); err != nil {
		t.Fatalf("idempotent ReconstructHistory: %v", err)
	}

	due, _ := repository.AddRound(ctx, organization.Name, base.Add(-time.Hour))
	_, _ = repository.AddRound(ctx, organization.Name, base.Add(24*time.Hour))
	claimed, err := repository.ClaimDueRounds(ctx, base, base.Add(-15*time.Minute), 10)
	if err != nil || len(claimed) != 1 || claimed[0].ID != due.ID {
		t.Fatalf("ClaimDueRounds = %#v, %v", claimed, err)
	}
	again, err := repository.ClaimDueRounds(ctx, base, base.Add(-15*time.Minute), 10)
	if err != nil || len(again) != 0 {
		t.Fatalf("second ClaimDueRounds = %#v, %v", again, err)
	}
	if err := repository.ReleaseRoundClaim(ctx, organization.Name, due.ID); err != nil {
		t.Fatalf("ReleaseRoundClaim: %v", err)
	}

	rollbackRound, _ := repository.AddRound(ctx, organization.Name, base.Add(-2*time.Hour))
	if _, err := db.ExecContext(ctx, `
		CREATE FUNCTION mealbot_test_fail_history() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected history failure'; END $$;
		CREATE TRIGGER mealbot_test_fail_history BEFORE UPDATE OF last_round_with ON members
		FOR EACH ROW EXECUTE FUNCTION mealbot_test_fail_history()`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	if err := repository.PersistPairings(ctx, organization.Name, rollbackRound.ID, []domain.Pair{pairAB}); err == nil {
		t.Fatal("PersistPairings succeeded despite injected SQL error")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER mealbot_test_fail_history ON members; DROP FUNCTION mealbot_test_fail_history()`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	var pairCount int
	var done bool
	if err := db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM pairs WHERE organization=$1 AND round=$2),
		       (SELECT done FROM rounds WHERE organization=$1 AND id=$2)`,
		organization.Name, rollbackRound.ID).Scan(&pairCount, &done); err != nil {
		t.Fatalf("read rollback state: %v", err)
	}
	if pairCount != 0 || done {
		t.Fatalf("failed pairing partially committed: pairs=%d done=%v", pairCount, done)
	}

	if _, err := db.ExecContext(ctx, `ALTER TABLE pairs DROP CONSTRAINT pairs_id2_fkey`); err != nil {
		t.Fatalf("drop member constraint for corruption test: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE rounds SET done = true WHERE organization = $1 AND id = $2`,
		organization.Name, rollbackRound.ID); err != nil {
		t.Fatalf("complete corrupt historical round: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO pairs (organization, round, id1, id2) VALUES ($1, $2, $3, $4)`,
		organization.Name, rollbackRound.ID, memberA.Email, "missing@example.test"); err != nil {
		t.Fatalf("insert corrupt historical pair: %v", err)
	}
	if _, err := repository.ListHistoricalPairs(ctx, organization.Name); !domain.IsIntegrity(err) {
		t.Fatalf("missing historical member error = %v, want integrity", err)
	}
}

func TestLegacyPairCountsMigration(t *testing.T) {
	if os.Getenv("RUN_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set RUN_POSTGRES_INTEGRATION=1 with a disposable DATABASE_URL")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if _, err := validatedTestDatabaseName(databaseURL); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := sql.Open(driverName, databaseURL)
	if err != nil {
		t.Fatalf("sql.Open legacy migration database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close legacy migration database: %v", err)
		}
	})
	acquirePostgresIntegrationSuiteLock(t, ctx, db)
	if _, err := db.ExecContext(ctx, resetSQL); err != nil {
		t.Fatalf("reset legacy migration database: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE organizations (name text PRIMARY KEY, admin text NOT NULL, cross_match_trait text);
		CREATE TABLE members (organization text, email text, name text, metadata jsonb,
			pair_counts jsonb NOT NULL, active boolean NOT NULL, PRIMARY KEY (organization,email));
		CREATE TABLE rounds (organization text, id integer, scheduled_date timestamp, done boolean,
			PRIMARY KEY (organization,id));
		CREATE TABLE pairs (organization text, id1 text, id2 text, extraId text NOT NULL,
			round integer, PRIMARY KEY (organization,id1,id2,extraId,round));
		INSERT INTO organizations VALUES ('legacy', 'admin', NULL);
		INSERT INTO members VALUES ('legacy','a@example.test','A','{"team":"red"}',
			'{"b@example.test":2}',true),
			('legacy','b@example.test','B','{}','{}',true),
			('legacy','c@example.test','C','{}','{}',true);
		INSERT INTO rounds VALUES ('legacy', 0, '2026-11-25 10:00:00', true);
		INSERT INTO pairs VALUES
			('legacy','b@example.test','c@example.test','a@example.test',0),
			('legacy','a@example.test','c@example.test','b@example.test',0),
			('legacy','a@example.test','b@example.test','c@example.test',0)`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	repository, _ := New(db)
	if err := repository.Migrate(ctx); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	if err := repository.Migrate(ctx); err != nil {
		t.Fatalf("idempotent legacy migration: %v", err)
	}
	members, err := repository.ListMembers(ctx, "legacy", false)
	if err != nil || len(members) != 3 || members[0].LastRoundWith["b@example.test"] != 2 || members[0].Metadata["team"] != "red" {
		t.Fatalf("legacy member after migration = %#v, %v", members, err)
	}

	var id1, id2 string
	var extraID sql.NullString
	var retained int
	if err := db.QueryRowContext(ctx, `
		SELECT MIN(id1), MIN(id2), MIN(extra_id), COUNT(*)
		FROM pairs WHERE organization = 'legacy' AND round = 0`,
	).Scan(&id1, &id2, &extraID, &retained); err != nil {
		t.Fatalf("read canonicalized legacy triple: %v", err)
	}
	if retained != 1 || id1 != "a@example.test" || id2 != "b@example.test" ||
		!extraID.Valid || extraID.String != "c@example.test" {
		t.Fatalf("canonicalized legacy triple = (%q,%q,%q) count %d", id1, id2, extraID.String, retained)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE pairs SET id1 = 'b@example.test', id2 = 'c@example.test', extra_id = 'a@example.test'
		WHERE organization = 'legacy' AND round = 0`); err == nil {
		t.Fatal("canonical order constraint accepted a non-canonical legacy triple")
	}
	historical, err := repository.ListHistoricalPairs(ctx, "legacy")
	if err != nil || len(historical) != 1 || len(historical[0].Pairs) != 1 || historical[0].Pairs[0].ExtraMember == nil {
		t.Fatalf("canonicalized legacy history = %#v, %v", historical, err)
	}
	if err := repository.ReconstructOrganizationHistory(ctx, "legacy"); err != nil {
		t.Fatalf("reconstruct canonicalized legacy history: %v", err)
	}
	members, err = repository.ListMembers(ctx, "legacy", false)
	if err != nil {
		t.Fatalf("read reconstructed legacy members: %v", err)
	}
	assertSymmetricRound(t, members, "a@example.test", "b@example.test", 0)
	assertSymmetricRound(t, members, "a@example.test", "c@example.test", 0)
	assertSymmetricRound(t, members, "b@example.test", "c@example.test", 0)
}

func acquirePostgresIntegrationSuiteLock(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	lockContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	connection, err := db.Conn(lockContext)
	if err != nil {
		t.Fatalf("open dedicated PostgreSQL integration lock session: %v", err)
	}
	if _, err := connection.ExecContext(lockContext, "SELECT pg_advisory_lock($1)", postgresIntegrationSuiteLockKey); err != nil {
		closeErr := connection.Close()
		t.Fatalf("acquire PostgreSQL integration advisory lock: %v; close lock session: %v", err, closeErr)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		var released bool
		if err := connection.QueryRowContext(cleanupContext,
			"SELECT pg_advisory_unlock($1)", postgresIntegrationSuiteLockKey,
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

func integrationMember(t *testing.T, organization, email, name string, metadata map[string]string) domain.Member {
	t.Helper()
	member, err := domain.NewMember(organization, email, name, metadata, nil)
	if err != nil {
		t.Fatalf("NewMember(%q): %v", email, err)
	}
	return member
}

func assertMemberState(t *testing.T, members []domain.Member, email string, active bool, name string) {
	t.Helper()
	for _, member := range members {
		if member.Email == email {
			if member.Active != active || member.Name != name {
				t.Fatalf("member %s = active %v name %q", email, member.Active, member.Name)
			}
			return
		}
	}
	t.Fatalf("member %s not found", email)
}

func assertSymmetricRound(t *testing.T, members []domain.Member, left, right string, round int) {
	t.Helper()
	values := map[string]domain.Member{}
	for _, member := range members {
		values[member.Email] = member
	}
	if values[left].LastRoundWith[right] != round || values[right].LastRoundWith[left] != round {
		t.Fatalf("history %s/%s is not symmetric at round %d: %#v", left, right, round, values)
	}
}

func TestIntegrationErrorClassificationContract(t *testing.T) {
	if !errors.Is(domain.NewIntegrityError("test", "test", nil), domain.ErrIntegrity) {
		t.Fatal("domain integrity error contract changed")
	}
}

func TestValidatedTestDatabaseName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "exact", raw: "postgresql://tester@localhost/test", want: "test"},
		{name: "prefix segment", raw: "postgresql://tester@localhost/test_mealbot", want: "test_mealbot"},
		{name: "middle segment", raw: "postgresql://tester@localhost/mealbot_test_123", want: "mealbot_test_123"},
		{name: "hyphen segment", raw: "postgresql://tester@localhost/mealbot-test", want: "mealbot-test"},
		{name: "socket query", raw: "postgresql:///mealbot_test?host=%2Fvar%2Frun%2Fpostgresql", want: "mealbot_test"},
		{name: "case insensitive", raw: "postgres://tester@localhost/MealBot_TEST?sslmode=disable", want: "MealBot_TEST"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := validatedTestDatabaseName(test.raw)
			if err != nil || got != test.want {
				t.Fatalf("validatedTestDatabaseName(%q) = %q, %v, want %q", test.raw, got, err, test.want)
			}
		})
	}
}

func TestValidatedTestDatabaseNameRejectsUnsafeURLs(t *testing.T) {
	t.Parallel()
	tests := []string{
		"",
		"localhost/mealbot_test",
		"mysql://tester@localhost/mealbot_test",
		"postgresql://tester@localhost/postgres",
		"postgresql://tester@localhost/latest",
		"postgresql://tester@localhost/contest",
		"postgresql://tester@localhost/testimonials",
		"postgresql://tester@localhost/mealbot_testimonials",
		"postgresql://tester@localhost/mealbot_test123",
		"postgresql://tester@localhost/mealbottest",
		"postgresql://tester@localhost/mealbot_test?dbname=production",
		"postgresql://tester@localhost/mealbot_test%ZZ",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if got, err := validatedTestDatabaseName(raw); err == nil {
				t.Fatalf("validatedTestDatabaseName(%q) = %q, want error", raw, got)
			}
		})
	}
}

func validatedTestDatabaseName(raw string) (string, error) {
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
