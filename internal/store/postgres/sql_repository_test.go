package postgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/domain"
)

func TestOrganizationRepositoryMethods(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	ctx := context.Background()
	rows := sqlmock.NewRows([]string{"name", "admin", "cross_match_trait"}).
		AddRow("alpha", "admin@example.test", nil).
		AddRow("beta", "admin@example.test", "team")
	mock.ExpectQuery("SELECT name, admin, cross_match_trait").WithArgs("admin@example.test").
		WillReturnRows(rows).
		RowsWillBeClosed()
	organizations, err := repository.ListOrganizations(ctx, " admin@example.test ")
	if err != nil || len(organizations) != 2 || organizations[1].CrossMatchTrait == nil {
		t.Fatalf("ListOrganizations = %#v, %v", organizations, err)
	}
	mock.ExpectQuery("SELECT name, admin, cross_match_trait FROM organizations ORDER BY name").
		WillReturnRows(sqlmock.NewRows([]string{"name", "admin", "cross_match_trait"}).AddRow("alpha", "admin@example.test", nil)).
		RowsWillBeClosed()
	all, err := repository.ListAllOrganizations(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListAllOrganizations = %#v, %v", all, err)
	}
	mock.ExpectExec("INSERT INTO organizations").WithArgs("gamma", "admin@example.test", nil).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repository.CreateOrganization(ctx, domain.Organization{Name: "gamma", Admin: "admin@example.test"}); err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	mock.ExpectQuery("SELECT cross_match_trait FROM organizations").WithArgs("gamma").
		WillReturnRows(sqlmock.NewRows([]string{"cross_match_trait"}).AddRow("team"))
	trait, err := repository.CrossMatchTrait(ctx, "gamma")
	if err != nil || trait == nil || *trait != "team" {
		t.Fatalf("CrossMatchTrait = %v, %v", trait, err)
	}
	mock.ExpectQuery("SELECT cross_match_trait FROM organizations").WithArgs("gamma").
		WillReturnRows(sqlmock.NewRows([]string{"cross_match_trait"}).AddRow(nil))
	trait, err = repository.CrossMatchTrait(ctx, "gamma")
	if err != nil || trait != nil {
		t.Fatalf("unset CrossMatchTrait = %v, %v", trait, err)
	}
	mock.ExpectExec("UPDATE organizations SET cross_match_trait").WithArgs("office", "gamma").WillReturnResult(sqlmock.NewResult(0, 1))
	updated := " office "
	if err := repository.UpdateCrossMatchTrait(ctx, "gamma", &updated); err != nil {
		t.Fatalf("UpdateCrossMatchTrait: %v", err)
	}
	mock.ExpectExec("UPDATE organizations SET cross_match_trait").WithArgs(nil, "missing").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repository.UpdateCrossMatchTrait(ctx, "missing", nil); !domain.IsNotFound(err) {
		t.Fatalf("missing UpdateCrossMatchTrait = %v", err)
	}
}

func TestListAndReconcileMembers(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	ctx := context.Background()
	columns := []string{"organization", "email", "name", "metadata", "active", "last_round_with"}
	mock.ExpectQuery("SELECT organization, email, name, metadata, active, last_round_with").WithArgs("org").
		WillReturnRows(sqlmock.NewRows(columns).AddRow("org", "a@example.test", "A", []byte(`{"team":"red"}`), true, []byte(`{}`)))
	members, err := repository.ListMembers(ctx, "org", true)
	if err != nil || len(members) != 1 || members[0].Metadata["team"] != "red" {
		t.Fatalf("ListMembers = %#v, %v", members, err)
	}

	memberA := mustMember(t, "org", "a@example.test", "A updated")
	memberB := mustMember(t, "org", "b@example.test", "B")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name FROM organizations").WithArgs("org").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("org"))
	mock.ExpectQuery("SELECT organization, email, name, metadata, active, last_round_with").WithArgs("org").
		WillReturnRows(sqlmock.NewRows(columns).AddRow("org", "a@example.test", "A", []byte(`{"old":"yes"}`), false, []byte(`{"z@example.test":3}`)))
	mock.ExpectExec("INSERT INTO members").WithArgs("org", "a@example.test", "A updated", sqlmock.AnyArg(), true, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO members").WithArgs("org", "b@example.test", "B", sqlmock.AnyArg(), true, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	active, err := repository.ReconcileMembers(ctx, "org", []domain.Member{memberA, memberB})
	if err != nil || len(active) != 2 {
		t.Fatalf("ReconcileMembers = %#v, %v", active, err)
	}
	if active[0].LastRoundWith["b@example.test"] != -1 || active[1].LastRoundWith["a@example.test"] != -1 {
		t.Fatalf("ReconcileMembers did not extend symmetric histories: %#v", active)
	}
}

func TestReconcileMembersRollsBackAfterPartialMutation(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	ctx := context.Background()
	columns := []string{"organization", "email", "name", "metadata", "active", "last_round_with"}
	memberA := mustMember(t, "org", "a@example.test", "A")
	memberB := mustMember(t, "org", "b@example.test", "B")
	injected := &pgconn.PgError{Code: "23514", Message: "injected member constraint failure"}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name FROM organizations").WithArgs("org").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("org"))
	mock.ExpectQuery("SELECT organization, email, name, metadata, active, last_round_with").WithArgs("org").
		WillReturnRows(sqlmock.NewRows(columns))
	mock.ExpectExec("INSERT INTO members").
		WithArgs("org", "a@example.test", "A", sqlmock.AnyArg(), true, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO members").
		WithArgs("org", "b@example.test", "B", sqlmock.AnyArg(), true, sqlmock.AnyArg()).
		WillReturnError(injected)
	mock.ExpectRollback()

	active, err := repository.ReconcileMembers(ctx, "org", []domain.Member{memberB, memberA})
	if active != nil {
		t.Fatalf("ReconcileMembers returned partial success: %#v", active)
	}
	if !domain.IsIntegrity(err) || domain.KindOf(err) != domain.ErrorKindIntegrity {
		t.Fatalf("ReconcileMembers error = %v, want integrity", err)
	}
	if !errors.Is(err, injected) {
		t.Fatalf("ReconcileMembers error does not wrap repository failure: %v", err)
	}
	var classified *domain.Error
	if !errors.As(err, &classified) || classified.Operation() != "save reconciled member" {
		t.Fatalf("ReconcileMembers classified error = %#v, want save operation", classified)
	}
}

func TestRoundRepositoryMethods(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	ctx := context.Background()
	when := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name FROM organizations").WithArgs("org").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("org"))
	mock.ExpectQuery("SELECT MAX\\(id\\)").WithArgs("org").WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	mock.ExpectExec("INSERT INTO rounds").WithArgs("org", 0, when).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	round, err := repository.AddRound(ctx, "org", when)
	if err != nil || round.ID != 0 {
		t.Fatalf("AddRound = %#v, %v", round, err)
	}
	mock.ExpectQuery("SELECT organization, id, scheduled_date, done").WithArgs("org").
		WillReturnRows(sqlmock.NewRows([]string{"organization", "id", "scheduled_date", "done"}).AddRow("org", 0, when, false))
	rounds, err := repository.ListRounds(ctx, "org")
	if err != nil || len(rounds) != 1 {
		t.Fatalf("ListRounds = %#v, %v", rounds, err)
	}
	mock.ExpectExec(`UPDATE rounds SET scheduled_date = \$1, processing_started_at = NULL, processing_token = NULL`).WithArgs(when.Add(time.Hour), "org", 0).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repository.RescheduleRound(ctx, "org", 0, when.Add(time.Hour)); err != nil {
		t.Fatalf("RescheduleRound: %v", err)
	}

	mock.ExpectQuery(`(?s)WITH due AS.*SET processing_started_at = \$1, processing_token = gen_random_uuid\(\)::text.*RETURNING.*processing_token`).WithArgs(when, when.Add(-time.Minute), 4).
		WillReturnRows(sqlmock.NewRows([]string{"organization", "id", "scheduled_date", "done", "processing_token"}).
			AddRow("z-org", 2, when.Add(-time.Hour), false, "token-z").
			AddRow("org", 3, when.Add(-2*time.Hour), false, "token-3").
			AddRow("org", 1, when.Add(-2*time.Hour), false, "token-1"))
	due, err := repository.ClaimDueRounds(ctx, when, when.Add(-time.Minute), 4)
	if err != nil || len(due) != 3 || due[0].ID != 1 || due[1].ID != 3 || due[2].Organization != "z-org" {
		t.Fatalf("ClaimDueRounds = %#v, %v", due, err)
	}
	if due[0].ClaimToken != "token-1" {
		t.Fatalf("first claim token = %q", due[0].ClaimToken)
	}
	mock.ExpectExec("UPDATE rounds SET processing_started_at = NULL, processing_token = NULL").WithArgs("org", 0, "token-0").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repository.ReleaseRoundClaim(ctx, "org", 0, "token-0"); err != nil {
		t.Fatalf("ReleaseRoundClaim: %v", err)
	}
}

func TestDeleteRoundCompactsAndReconstructs(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	ctx := context.Background()
	memberColumns := []string{"organization", "email", "name", "metadata", "active", "last_round_with"}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name FROM organizations").WithArgs("org").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("org"))
	mock.ExpectQuery(`(?s)SELECT id, processing_token.*id >= \$2.*ORDER BY id.*FOR UPDATE`).WithArgs("org", 1).WillReturnRows(
		sqlmock.NewRows([]string{"id", "processing_token"}).AddRow(1, nil).AddRow(2, nil).AddRow(3, nil))
	mock.ExpectExec("DELETE FROM pairs").WithArgs("org", 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM rounds").WithArgs("org", 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE rounds SET id").WithArgs(1, "org", 2).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE rounds SET id").WithArgs(2, "org", 3).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT organization, email, name, metadata, active, last_round_with").WithArgs("org").WillReturnRows(sqlmock.NewRows(memberColumns))
	mock.ExpectQuery("SELECT round, id1, id2, extra_id").WithArgs("org").WillReturnRows(sqlmock.NewRows([]string{"round", "id1", "id2", "extra_id"}))
	mock.ExpectCommit()
	if err := repository.DeleteRound(ctx, "org", 1); err != nil {
		t.Fatalf("DeleteRound: %v", err)
	}
}

func TestDeleteRoundRejectsActiveClaimBeforeMutation(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name FROM organizations").WithArgs("org").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("org"))
	mock.ExpectQuery(`(?s)SELECT id, processing_token.*id >= \$2.*ORDER BY id.*FOR UPDATE`).WithArgs("org", 1).WillReturnRows(
		sqlmock.NewRows([]string{"id", "processing_token"}).
			AddRow(1, nil).
			AddRow(2, "active-token").
			AddRow(3, nil))
	mock.ExpectRollback()
	if err := repository.DeleteRound(context.Background(), "org", 1); !domain.IsConflict(err) {
		t.Fatalf("DeleteRound active claim = %v, want conflict", err)
	}
}

func TestPairRepositoryMethods(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	ctx := context.Background()
	when := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	pairRows := sqlmock.NewRows([]string{"organization", "id", "scheduled_date", "done", "id1", "id2", "extra_id"}).
		AddRow("org", 0, when, true, "a@example.test", "b@example.test", nil)
	mock.ExpectQuery("SELECT r.organization").WithArgs("org").WillReturnRows(pairRows)
	rounds, err := repository.ListPairs(ctx, "org")
	if err != nil || len(rounds) != 1 || len(rounds[0].Pairs) != 1 {
		t.Fatalf("ListPairs = %#v, %v", rounds, err)
	}

	pair, _ := domain.NewPair("a@example.test", "b@example.test", "c@example.test")
	memberColumns := []string{"organization", "email", "name", "metadata", "active", "last_round_with"}
	memberRows := sqlmock.NewRows(memberColumns).
		AddRow("org", "a@example.test", "A", []byte(`{}`), true, []byte(`{}`)).
		AddRow("org", "b@example.test", "B", []byte(`{}`), true, []byte(`{}`)).
		AddRow("org", "c@example.test", "C", []byte(`{}`), false, []byte(`{}`))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT done FROM rounds").WithArgs("org", 2, "claim-2").WillReturnRows(sqlmock.NewRows([]string{"done"}).AddRow(false))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM pairs").WithArgs("org", 2).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT organization, email, name, metadata, active, last_round_with").WithArgs("org").WillReturnRows(memberRows)
	mock.ExpectExec("INSERT INTO pairs").WithArgs("org", pair.ID1, pair.ID2, pair.ExtraID, 2).WillReturnResult(sqlmock.NewResult(0, 1))
	for _, email := range pair.Members() {
		mock.ExpectExec("UPDATE members SET last_round_with").WithArgs(sqlmock.AnyArg(), "org", email).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(`(?s)UPDATE rounds.*processing_token = NULL.*processing_token = \$3`).WithArgs("org", 2, "claim-2").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repository.PersistPairings(ctx, "org", 2, "claim-2", []domain.Pair{pair}); err != nil {
		t.Fatalf("PersistPairings: %v", err)
	}
}

func TestListPairsPreservesCompletedEmptyRoundsAndExcludesUnfinishedRounds(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	ctx := context.Background()
	when := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	columns := []string{"organization", "id", "scheduled_date", "done", "id1", "id2", "extra_id"}
	rows := sqlmock.NewRows(columns).
		AddRow("org", 0, when, true, nil, nil, nil).
		AddRow("org", 2, when.Add(2*time.Hour), true, "a@example.test", "b@example.test", nil).
		AddRow("org", 2, when.Add(2*time.Hour), true, "c@example.test", "d@example.test", "e@example.test")
	mock.ExpectQuery(`(?s)FROM rounds r\s+LEFT JOIN pairs p ON p\.organization = r\.organization AND p\.round = r\.id\s+WHERE r\.organization = \$1 AND r\.done = true\s+ORDER BY r\.id, p\.id1, p\.id2`).
		WithArgs("org").
		WillReturnRows(rows)

	rounds, err := repository.ListPairs(ctx, "org")
	if err != nil {
		t.Fatalf("ListPairs: %v", err)
	}
	if len(rounds) != 2 || rounds[0].ID != 0 || rounds[1].ID != 2 {
		t.Fatalf("ListPairs rounds = %#v", rounds)
	}
	if rounds[0].Pairs == nil || len(rounds[0].Pairs) != 0 {
		t.Fatalf("empty completed round pairs = %#v, want non-nil empty slice", rounds[0].Pairs)
	}
	wantPairs := []domain.Pair{
		{ID1: "a@example.test", ID2: "b@example.test"},
		{ID1: "c@example.test", ID2: "d@example.test", ExtraID: "e@example.test"},
	}
	if !reflect.DeepEqual(rounds[1].Pairs, wantPairs) {
		t.Fatalf("later round pairs = %#v, want %#v", rounds[1].Pairs, wantPairs)
	}
}

func TestListHistoricalPairsIncludesInactiveMembers(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	ctx := context.Background()
	when := time.Now().UTC()
	mock.ExpectQuery("SELECT r.organization").WithArgs("org").WillReturnRows(
		sqlmock.NewRows([]string{"organization", "id", "scheduled_date", "done", "id1", "id2", "extra_id"}).
			AddRow("org", 0, when, true, nil, nil, nil).
			AddRow("org", 1, when.Add(time.Hour), true, "a@example.test", "b@example.test", nil))
	mock.ExpectQuery("SELECT organization, email, name, metadata, active, last_round_with").WithArgs("org").WillReturnRows(
		sqlmock.NewRows([]string{"organization", "email", "name", "metadata", "active", "last_round_with"}).
			AddRow("org", "a@example.test", "A", []byte(`{}`), true, []byte(`{}`)).
			AddRow("org", "b@example.test", "B", []byte(`{}`), false, []byte(`{}`)))
	history, err := repository.ListHistoricalPairs(ctx, "org")
	if err != nil || len(history) != 2 || history[0].Pairs == nil || len(history[0].Pairs) != 0 || history[1].Pairs[0].Member2.Active {
		t.Fatalf("ListHistoricalPairs = %#v, %v", history, err)
	}
}

func TestReconstructHistory(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	ctx := context.Background()
	mock.ExpectQuery("SELECT name, admin, cross_match_trait FROM organizations ORDER BY name").WillReturnRows(
		sqlmock.NewRows([]string{"name", "admin", "cross_match_trait"}).AddRow("org", "admin", nil))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name FROM organizations").WithArgs("org").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("org"))
	mock.ExpectQuery("SELECT organization, email, name, metadata, active, last_round_with").WithArgs("org").WillReturnRows(
		sqlmock.NewRows([]string{"organization", "email", "name", "metadata", "active", "last_round_with"}).
			AddRow("org", "a@example.test", "A", []byte(`{}`), true, []byte(`{}`)).
			AddRow("org", "b@example.test", "B", []byte(`{}`), false, []byte(`{}`)))
	mock.ExpectQuery("SELECT round, id1, id2, extra_id").WithArgs("org").WillReturnRows(
		sqlmock.NewRows([]string{"round", "id1", "id2", "extra_id"}).AddRow(3, "a@example.test", "b@example.test", nil))
	mock.ExpectExec("UPDATE members SET last_round_with").WithArgs(sqlmock.AnyArg(), "org", "a@example.test").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE members SET last_round_with").WithArgs(sqlmock.AnyArg(), "org", "b@example.test").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repository.ReconstructHistory(ctx); err != nil {
		t.Fatalf("ReconstructHistory: %v", err)
	}
}

func TestMigrateExecutesOrderedLedgerTransactions(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	for version := 1; version <= 4; version++ {
		mock.ExpectBegin()
		mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(migrationLockID).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS mealbot_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery("SELECT EXISTS").WithArgs(version).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectExec("(?s).+").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("INSERT INTO mealbot_schema_migrations").WithArgs(version, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}
	if err := repository.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}

func TestConstraintsMigrationCanonicalizesBeforePrimaryKey(t *testing.T) {
	t.Parallel()
	entries, err := migrationEntries()
	if err != nil {
		t.Fatalf("migrationEntries: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("migration count = %d, want 4", len(entries))
	}
	migration := entries[2].sql
	markers := []string{
		"ALTER TABLE pairs DROP CONSTRAINT IF EXISTS pairs_pkey",
		"CREATE TEMPORARY TABLE mealbot_canonical_pairs ON COMMIT DROP AS",
		"SELECT DISTINCT",
		"unnest(ARRAY[pairs.id1, pairs.id2, pairs.extra_id])",
		"DELETE FROM pairs",
		"INSERT INTO pairs (organization, round, id1, id2, extra_id)",
		"ALTER TABLE pairs ADD CONSTRAINT pairs_pkey",
		"ALTER TABLE pairs ADD CONSTRAINT pairs_canonical_order_check",
	}
	position := -1
	for _, marker := range markers {
		next := strings.Index(migration, marker)
		if next <= position {
			t.Fatalf("migration marker %q at %d, want after %d", marker, next, position)
		}
		position = next
	}
}

func TestStableRoundClaimsMigrationRepairsPartialClaimsBeforeConstraint(t *testing.T) {
	t.Parallel()
	entries, err := migrationEntries()
	if err != nil {
		t.Fatalf("migrationEntries: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("migration count = %d, want 4", len(entries))
	}
	migration := entries[3].sql
	markers := []string{
		"ALTER TABLE rounds ADD COLUMN IF NOT EXISTS processing_token",
		"UPDATE rounds",
		"WHERE (processing_started_at IS NULL) <> (processing_token IS NULL)",
		"OR length(btrim(processing_token)) = 0",
		"DO $migration$",
		"SELECT 1 FROM pg_constraint",
		"rounds_processing_claim_consistent",
		"length(btrim(processing_token)) > 0",
	}
	position := -1
	for _, marker := range markers {
		next := strings.Index(migration, marker)
		if next <= position {
			t.Fatalf("migration marker %q at %d, want after %d", marker, next, position)
		}
		position = next
	}
}

func TestRepositoryValidationAndErrorPaths(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	ctx := context.Background()
	if _, err := repository.ListOrganizations(ctx, " "); !domain.IsValidation(err) {
		t.Fatalf("empty admin = %v", err)
	}
	if err := repository.CreateOrganization(ctx, domain.Organization{}); !domain.IsValidation(err) {
		t.Fatalf("invalid organization create = %v", err)
	}
	if _, err := repository.CrossMatchTrait(ctx, " "); !domain.IsValidation(err) {
		t.Fatalf("empty trait organization = %v", err)
	}
	if _, err := repository.ListMembers(ctx, " ", false); !domain.IsValidation(err) {
		t.Fatalf("empty member organization = %v", err)
	}
	if _, err := repository.ListPairs(ctx, " "); !domain.IsValidation(err) {
		t.Fatalf("empty pair organization = %v", err)
	}
	if _, err := repository.ListHistoricalPairs(ctx, " "); !domain.IsValidation(err) {
		t.Fatalf("empty historical organization = %v", err)
	}
	if _, err := repository.ReconcileMembers(ctx, "org", nil); !domain.IsValidation(err) {
		t.Fatalf("empty reconciliation = %v", err)
	}
	if _, err := repository.ReconcileMembers(ctx, " ", []domain.Member{{}}); !domain.IsValidation(err) {
		t.Fatalf("empty reconcile organization = %v", err)
	}
	wrongOrg := mustMember(t, "wrong", "a@example.test", "A")
	if _, err := repository.ReconcileMembers(ctx, "org", []domain.Member{wrongOrg}); !domain.IsValidation(err) {
		t.Fatalf("mismatched reconcile organization = %v", err)
	}
	duplicate := mustMember(t, "org", "a@example.test", "A")
	if _, err := repository.ReconcileMembers(ctx, "org", []domain.Member{duplicate, duplicate}); !domain.IsConflict(err) {
		t.Fatalf("duplicate reconciliation = %v", err)
	}
	if _, err := repository.AddRound(ctx, "org", time.Time{}); !domain.IsValidation(err) {
		t.Fatalf("zero scheduled time = %v", err)
	}
	if _, err := repository.AddRound(ctx, " ", time.Now()); !domain.IsValidation(err) {
		t.Fatalf("empty add-round organization = %v", err)
	}
	if _, err := repository.ListRounds(ctx, " "); !domain.IsValidation(err) {
		t.Fatalf("empty list-round organization = %v", err)
	}
	if err := repository.RescheduleRound(ctx, "org", -1, time.Now()); !domain.IsValidation(err) {
		t.Fatalf("negative round = %v", err)
	}
	if err := repository.DeleteRound(ctx, "org", -1); !domain.IsValidation(err) {
		t.Fatalf("negative deleted round = %v", err)
	}
	if err := repository.ReconstructOrganizationHistory(ctx, " "); !domain.IsValidation(err) {
		t.Fatalf("empty reconstruction organization = %v", err)
	}
	if _, err := repository.ClaimDueRounds(ctx, time.Time{}, time.Now(), 1); !domain.IsValidation(err) {
		t.Fatalf("zero due clock = %v", err)
	}
	if _, err := repository.ClaimDueRounds(ctx, time.Now(), time.Now().Add(time.Hour), 1); !domain.IsValidation(err) {
		t.Fatalf("future stale cutoff = %v", err)
	}
	if _, err := repository.ClaimDueRounds(ctx, time.Now(), time.Now().Add(-time.Hour), 0); !domain.IsValidation(err) {
		t.Fatalf("zero due limit = %v", err)
	}
	if err := repository.PersistPairings(ctx, "org", -1, "token", nil); !domain.IsValidation(err) {
		t.Fatalf("negative persisted round = %v", err)
	}
	if err := repository.PersistPairings(ctx, "org", 0, " ", nil); !domain.IsValidation(err) {
		t.Fatalf("empty persistence claim token = %v", err)
	}
	pair, _ := domain.NewPair("a@example.test", "b@example.test")
	if err := repository.PersistPairings(ctx, "org", 0, "token", []domain.Pair{pair, pair}); !domain.IsConflict(err) {
		t.Fatalf("duplicate pair = %v", err)
	}
	mock.ExpectQuery("SELECT cross_match_trait FROM organizations").WithArgs("missing").WillReturnError(sql.ErrNoRows)
	if _, err := repository.CrossMatchTrait(ctx, "missing"); !domain.IsNotFound(err) {
		t.Fatalf("missing trait = %v", err)
	}
	if err := repository.ReleaseRoundClaim(ctx, "org", 9, " "); !domain.IsValidation(err) {
		t.Fatalf("empty release token = %v", err)
	}
	mock.ExpectExec("UPDATE rounds SET processing_started_at = NULL, processing_token = NULL").WithArgs("org", 9, "stale-token").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repository.ReleaseRoundClaim(ctx, "org", 9, "stale-token"); !domain.IsConflict(err) {
		t.Fatalf("stale release claim = %v", err)
	}
}

func TestRepositoryPingCloseAndOpenValidation(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	repository, _ := New(db)
	mock.ExpectPing()
	if err := repository.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	mock.ExpectClose()
	if err := repository.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := Open(context.Background(), config.Database{}); !domain.IsValidation(err) {
		t.Fatalf("Open invalid config = %v", err)
	}
	var nilRepository *Repository
	if err := nilRepository.Ping(context.Background()); err == nil {
		t.Fatal("nil Ping succeeded")
	}
}

func TestOpenWithChecksPool(t *testing.T) {
	t.Parallel()
	database := config.Database{URL: "postgresql://db.example.test/mealbot"}
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectPing()
	repository, err := openWith(context.Background(), database, func(driver, dsn string) (*sql.DB, error) {
		if driver != driverName || dsn != database.URL {
			t.Fatalf("opener received %q %q", driver, dsn)
		}
		return db, nil
	})
	if err != nil || repository == nil {
		t.Fatalf("openWith success = %#v, %v", repository, err)
	}
	mock.ExpectClose()
	_ = db.Close()

	openFailure := errors.New("open failure")
	if _, err := openWith(context.Background(), database, func(string, string) (*sql.DB, error) {
		return nil, openFailure
	}); !errors.Is(err, openFailure) {
		t.Fatalf("openWith open error = %v", err)
	}

	failingDB, failingMock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	pingFailure := errors.New("ping failure")
	failingMock.ExpectPing().WillReturnError(pingFailure)
	failingMock.ExpectClose()
	if _, err := openWith(context.Background(), database, func(string, string) (*sql.DB, error) {
		return failingDB, nil
	}); !errors.Is(err, pingFailure) {
		t.Fatalf("openWith ping error = %v", err)
	}
}

func TestFindDueRoundsUsesCallerClock(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	cutoff := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`(?s)WITH due AS.*SET processing_started_at = \$1, processing_token = gen_random_uuid\(\)::text.*RETURNING.*processing_token`).WithArgs(cutoff, cutoff.Add(-defaultClaimLease), 100).
		WillReturnRows(sqlmock.NewRows([]string{"organization", "id", "scheduled_date", "done", "processing_token"}))
	rounds, err := repository.FindDueRounds(context.Background(), cutoff)
	if err != nil || len(rounds) != 0 {
		t.Fatalf("FindDueRounds = %#v, %v", rounds, err)
	}
}

func TestCompletedPairingRollsBack(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	pair, _ := domain.NewPair("a@example.test", "b@example.test")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT done FROM rounds").WithArgs("org", 1, "claim-1").
		WillReturnRows(sqlmock.NewRows([]string{"done"}).AddRow(true))
	mock.ExpectRollback()
	err := repository.PersistPairings(context.Background(), "org", 1, "claim-1", []domain.Pair{pair})
	if !domain.IsConflict(err) {
		t.Fatalf("completed PersistPairings = %v", err)
	}
}

func TestPersistPairingsRejectsStaleClaimBeforeWrites(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT done FROM rounds").WithArgs("org", 1, "stale-token").WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	if err := repository.PersistPairings(context.Background(), "org", 1, "stale-token", nil); !domain.IsConflict(err) {
		t.Fatalf("stale PersistPairings = %v, want conflict", err)
	}
}

func TestPersistPairingsRejectsMissingMemberAndPartialState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("missing member", func(t *testing.T) {
		repository, mock := mockRepository(t)
		pair, _ := domain.NewPair("a@example.test", "b@example.test")
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT done FROM rounds").WithArgs("org", 1, "claim-1").WillReturnRows(sqlmock.NewRows([]string{"done"}).AddRow(false))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM pairs").WithArgs("org", 1).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery("SELECT organization, email, name, metadata, active, last_round_with").WithArgs("org").WillReturnRows(
			sqlmock.NewRows([]string{"organization", "email", "name", "metadata", "active", "last_round_with"}).
				AddRow("org", "a@example.test", "A", []byte(`{}`), true, []byte(`{}`)))
		mock.ExpectRollback()
		if err := repository.PersistPairings(ctx, "org", 1, "claim-1", []domain.Pair{pair}); !domain.IsIntegrity(err) {
			t.Fatalf("missing member PersistPairings = %v", err)
		}
	})
	t.Run("partial state", func(t *testing.T) {
		repository, mock := mockRepository(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT done FROM rounds").WithArgs("org", 1, "claim-1").WillReturnRows(sqlmock.NewRows([]string{"done"}).AddRow(false))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM pairs").WithArgs("org", 1).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectRollback()
		if err := repository.PersistPairings(ctx, "org", 1, "claim-1", nil); !domain.IsIntegrity(err) {
			t.Fatalf("partial PersistPairings = %v", err)
		}
	})
}

func TestAddRoundAfterExistingMaximum(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	when := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name FROM organizations").WithArgs("org").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("org"))
	mock.ExpectQuery("SELECT MAX\\(id\\)").WithArgs("org").WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(4))
	mock.ExpectExec("INSERT INTO rounds").WithArgs("org", 5, when).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	round, err := repository.AddRound(context.Background(), "org", when)
	if err != nil || round.ID != 5 {
		t.Fatalf("AddRound after max = %#v, %v", round, err)
	}
}

func TestDeleteRoundNotFoundRollsBack(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name FROM organizations").WithArgs("org").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("org"))
	mock.ExpectQuery(`(?s)SELECT id, processing_token.*id >= \$2.*ORDER BY id.*FOR UPDATE`).WithArgs("org", 9).WillReturnRows(sqlmock.NewRows([]string{"id", "processing_token"}))
	mock.ExpectRollback()
	if err := repository.DeleteRound(context.Background(), "org", 9); !domain.IsNotFound(err) {
		t.Fatalf("DeleteRound missing = %v", err)
	}
}

func TestMigrateSkipsAppliedVersions(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	for version := 1; version <= 4; version++ {
		mock.ExpectBegin()
		mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs(migrationLockID).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS mealbot_schema_migrations").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery("SELECT EXISTS").WithArgs(version).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectCommit()
	}
	if err := repository.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate applied versions: %v", err)
	}
}

func TestHistoricalMissingMemberIsIntegrityError(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	when := time.Now().UTC()
	mock.ExpectQuery("SELECT r.organization").WithArgs("org").WillReturnRows(
		sqlmock.NewRows([]string{"organization", "id", "scheduled_date", "done", "id1", "id2", "extra_id"}).
			AddRow("org", 0, when, true, "a@example.test", "missing@example.test", nil))
	mock.ExpectQuery("SELECT organization, email, name, metadata, active, last_round_with").WithArgs("org").WillReturnRows(
		sqlmock.NewRows([]string{"organization", "email", "name", "metadata", "active", "last_round_with"}).
			AddRow("org", "a@example.test", "A", []byte(`{}`), true, []byte(`{}`)))
	if _, err := repository.ListHistoricalPairs(context.Background(), "org"); !domain.IsIntegrity(err) {
		t.Fatalf("ListHistoricalPairs missing member = %v", err)
	}
}

func TestReconstructOrganizationHistoryEmpty(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name FROM organizations").WithArgs("org").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("org"))
	mock.ExpectQuery("SELECT organization, email, name, metadata, active, last_round_with").WithArgs("org").WillReturnRows(
		sqlmock.NewRows([]string{"organization", "email", "name", "metadata", "active", "last_round_with"}))
	mock.ExpectQuery("SELECT round, id1, id2, extra_id").WithArgs("org").WillReturnRows(
		sqlmock.NewRows([]string{"round", "id1", "id2", "extra_id"}))
	mock.ExpectCommit()
	if err := repository.ReconstructOrganizationHistory(context.Background(), "org"); err != nil {
		t.Fatalf("ReconstructOrganizationHistory: %v", err)
	}
}

func mockRepository(t *testing.T) (*Repository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	mock.MatchExpectationsInOrder(true)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet SQL expectations: %v", err)
		}
		_ = db.Close()
	})
	repository, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return repository, mock
}

func mustMember(t *testing.T, organization, email, name string) domain.Member {
	t.Helper()
	member, err := domain.NewMember(organization, email, name, map[string]string{}, nil)
	if err != nil {
		t.Fatalf("NewMember: %v", err)
	}
	return member
}

func TestRowsErrorIsPropagated(t *testing.T) {
	t.Parallel()
	repository, mock := mockRepository(t)
	rowErr := errors.New("stream interrupted")
	rows := sqlmock.NewRows([]string{"name", "admin", "cross_match_trait"}).
		AddRow("alpha", "admin", nil).
		RowError(0, rowErr)
	mock.ExpectQuery("SELECT name, admin, cross_match_trait").WithArgs("admin").WillReturnRows(rows)
	if _, err := repository.ListOrganizations(context.Background(), "admin"); !errors.Is(err, rowErr) {
		t.Fatalf("ListOrganizations rows error = %v", err)
	}
	allRows := sqlmock.NewRows([]string{"name", "admin", "cross_match_trait"}).
		AddRow("alpha", "admin", nil).
		RowError(0, rowErr)
	mock.ExpectQuery("SELECT name, admin, cross_match_trait FROM organizations ORDER BY name").WillReturnRows(allRows)
	if _, err := repository.ListAllOrganizations(context.Background()); !errors.Is(err, rowErr) {
		t.Fatalf("ListAllOrganizations rows error = %v", err)
	}
	pairRows := sqlmock.NewRows([]string{"organization", "id", "scheduled_date", "done", "id1", "id2", "extra_id"}).
		AddRow("org", 0, time.Now().UTC(), true, nil, nil, nil).
		RowError(0, rowErr)
	mock.ExpectQuery("SELECT r.organization").WithArgs("org").WillReturnRows(pairRows)
	if _, err := repository.ListPairs(context.Background(), "org"); !errors.Is(err, rowErr) {
		t.Fatalf("ListPairs rows error = %v", err)
	}
}
