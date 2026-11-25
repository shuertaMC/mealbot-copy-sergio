package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/johnamadeo/mealbot/internal/domain"
)

//go:embed testdata/reset.sql
var resetSQL string

func TestPostgresRepositoryIntegration(t *testing.T) {
	if os.Getenv("RUN_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set RUN_POSTGRES_INTEGRATION=1 with a disposable DATABASE_URL")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	parsed, err := url.Parse(databaseURL)
	if err != nil || databaseURL == "" {
		t.Fatal("DATABASE_URL must be a valid disposable PostgreSQL URL")
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if !strings.Contains(strings.ToLower(databaseName), "test") {
		t.Fatalf("refusing destructive integration test against database %q: name must contain test", databaseName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := sql.Open(driverName, databaseURL)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	newTime := base.Add(90 * time.Minute)
	if err := repository.RescheduleRound(ctx, organization.Name, round1.ID, newTime); err != nil {
		t.Fatalf("RescheduleRound: %v", err)
	}
	pairAB, _ := domain.NewPair(memberA.Email, memberB.Email)
	triple, _ := domain.NewPair(memberA.Email, memberB.Email, memberC.Email)
	if err := repository.PersistPairings(ctx, organization.Name, round1.ID, []domain.Pair{pairAB}); err != nil {
		t.Fatalf("PersistPairings pair: %v", err)
	}
	if err := repository.PersistPairings(ctx, organization.Name, round2.ID, []domain.Pair{triple}); err != nil {
		t.Fatalf("PersistPairings triple: %v", err)
	}
	if err := repository.DeleteRound(ctx, organization.Name, round0.ID); err != nil {
		t.Fatalf("DeleteRound: %v", err)
	}
	rounds, err := repository.ListRounds(ctx, organization.Name)
	if err != nil || len(rounds) != 2 || rounds[0].ID != 0 || rounds[1].ID != 1 {
		t.Fatalf("compacted ListRounds = %#v, %v", rounds, err)
	}
	pairRounds, err := repository.ListPairs(ctx, organization.Name)
	if err != nil || len(pairRounds) != 2 || pairRounds[0].ID != 0 || pairRounds[1].ID != 1 {
		t.Fatalf("compacted ListPairs = %#v, %v", pairRounds, err)
	}
	allMembers, _ = repository.ListMembers(ctx, organization.Name, false)
	assertSymmetricRound(t, allMembers, memberA.Email, memberB.Email, 1)
	assertSymmetricRound(t, allMembers, memberA.Email, memberC.Email, 1)

	if _, err := repository.ReconcileMembers(ctx, organization.Name, []domain.Member{memberA, memberC}); err != nil {
		t.Fatalf("deactivate historical member: %v", err)
	}
	historical, err := repository.ListHistoricalPairs(ctx, organization.Name)
	if err != nil || len(historical) != 2 || historical[0].Pairs[0].Member2.Active {
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
	parsed, _ := url.Parse(databaseURL)
	if !strings.Contains(strings.ToLower(strings.TrimPrefix(parsed.Path, "/")), "test") {
		t.Skip("disposable database name must contain test")
	}
	ctx := context.Background()
	db, err := sql.Open(driverName, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, resetSQL); err != nil {
		t.Fatal(err)
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
			'{"b@example.test":2}',true)`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	repository, _ := New(db)
	if err := repository.Migrate(ctx); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}
	members, err := repository.ListMembers(ctx, "legacy", false)
	if err != nil || len(members) != 1 || members[0].LastRoundWith["b@example.test"] != 2 || members[0].Metadata["team"] != "red" {
		t.Fatalf("legacy member after migration = %#v, %v", members, err)
	}
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
