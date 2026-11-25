package store

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/johnamadeo/mealbot/internal/pairing"
)

func TestRoundAndPairRepositoriesPostgreSQL(t *testing.T) {
	withIsolatedIntegrationDB(t, func(ctx context.Context, db *sql.DB) {
		repository := migratedStore(t, ctx, db)
		seedOrganizationAndMembers(t, ctx, db, "rounds", "a@example.com", "b@example.com", "c@example.com", "d@example.com")

		for index, scheduled := range []string{
			"2026-01-01 01:02:03",
			"2026-01-02 02:03:04",
			"2026-01-03 03:04:05",
		} {
			id, err := repository.AddRound(ctx, "rounds", scheduled)
			if err != nil {
				t.Fatal(err)
			}
			if id != index {
				t.Fatalf("AddRound() ID = %d, want %d", id, index)
			}
		}
		listed, err := repository.ListRounds(ctx, "rounds")
		if err != nil {
			t.Fatal(err)
		}
		wantListed := []string{"2026-01-01 01:02:03Z", "2026-01-02 02:03:04Z", "2026-01-03 03:04:05Z"}
		if !reflect.DeepEqual(listed, wantListed) {
			t.Fatalf("ListRounds() = %v, want %v", listed, wantListed)
		}
		if err := repository.RescheduleRound(ctx, "rounds", "2026-02-02 12:13:14", 1); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO pairs (organization, id1, id2, extraid, round)
VALUES ('rounds', 'a@example.com', 'b@example.com', NULL, 2)
`); err != nil {
			t.Fatal(err)
		}
		if err := repository.DeleteRound(ctx, "rounds", 1); err != nil {
			t.Fatal(err)
		}
		var ids []int
		rows, err := db.QueryContext(ctx, `SELECT id FROM rounds WHERE organization = 'rounds' ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(ids, []int{0, 1}) {
			t.Fatalf("renumbered round IDs = %v", ids)
		}
		var pairRound int
		if err := db.QueryRowContext(ctx, `SELECT round FROM pairs WHERE organization = 'rounds'`).Scan(&pairRound); err != nil {
			t.Fatal(err)
		}
		if pairRound != 1 {
			t.Fatalf("renumbered pair round = %d, want 1", pairRound)
		}

		seedOrganizationAndMembers(t, ctx, db, "due", "a@example.com", "b@example.com")
		boundary := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
		for _, values := range []struct {
			id        int
			scheduled time.Time
			done      bool
		}{
			{id: 0, scheduled: boundary.Add(-time.Second)},
			{id: 1, scheduled: boundary},
			{id: 2, scheduled: boundary.Add(time.Second)},
			{id: 3, scheduled: boundary.Add(-time.Hour), done: true},
		} {
			if _, err := db.ExecContext(ctx, `
INSERT INTO rounds (organization, id, scheduled_date, done) VALUES ($1, $2, $3, $4)
`, "due", values.id, values.scheduled, values.done); err != nil {
				t.Fatal(err)
			}
		}
		due, err := repository.DueRounds(ctx, boundary)
		if err != nil {
			t.Fatal(err)
		}
		wantDue := []pairing.ScheduledRound{{Organization: "due", Number: 0}}
		if !reflect.DeepEqual(due, wantDue) {
			t.Fatalf("DueRounds() = %#v, want %#v", due, wantDue)
		}
	})
}

func TestRoundMutationsPostgreSQLRollBack(t *testing.T) {
	withIsolatedIntegrationDB(t, func(ctx context.Context, db *sql.DB) {
		repository := migratedStore(t, ctx, db)
		seedOrganizationAndMembers(t, ctx, db, "rollback-rounds", "a@example.com", "b@example.com")
		for id := 0; id < 3; id++ {
			if _, err := repository.AddRound(ctx, "rollback-rounds", "2026-01-01 00:00:00"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := repository.AddRound(ctx, "rollback-rounds", "not-a-timestamp"); err == nil {
			t.Fatal("AddRound() accepted an invalid timestamp")
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM rounds WHERE organization = 'rollback-rounds'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("failed add changed round count to %d", count)
		}

		if err := repository.RescheduleRound(ctx, "rollback-rounds", "not-a-timestamp", 1); err == nil {
			t.Fatal("RescheduleRound() accepted an invalid timestamp")
		}
		var scheduled string
		if err := db.QueryRowContext(ctx, `SELECT scheduled_date::text FROM rounds WHERE organization = 'rollback-rounds' AND id = 1`).Scan(&scheduled); err != nil {
			t.Fatal(err)
		}
		if scheduled != "2026-01-01 00:00:00" {
			t.Fatalf("failed reschedule changed timestamp to %q", scheduled)
		}

		if _, err := db.ExecContext(ctx, `
INSERT INTO pairs (organization, id1, id2, extraid, round)
VALUES ('rollback-rounds', 'a@example.com', 'b@example.com', NULL, 2)
`); err != nil {
			t.Fatal(err)
		}
		installFailureTrigger(t, ctx, db, "pairs", "fail_pair_renumber", "UPDATE")
		if err := repository.DeleteRound(ctx, "rollback-rounds", 1); err == nil {
			t.Fatal("DeleteRound() succeeded, want injected renumber failure")
		}
		rows, err := db.QueryContext(ctx, `SELECT id FROM rounds WHERE organization = 'rollback-rounds' ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		var ids []int
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(ids, []int{0, 1, 2}) {
			t.Fatalf("failed delete left IDs %v", ids)
		}
		var pairRound int
		if err := db.QueryRowContext(ctx, `SELECT round FROM pairs WHERE organization = 'rollback-rounds'`).Scan(&pairRound); err != nil {
			t.Fatal(err)
		}
		if pairRound != 2 {
			t.Fatalf("failed delete changed pair round to %d", pairRound)
		}
	})
}

func TestPairHistoryPostgreSQLGapAndExtraIDCompatibility(t *testing.T) {
	withIsolatedIntegrationDB(t, func(ctx context.Context, db *sql.DB) {
		repository := migratedStore(t, ctx, db)
		seedOrganizationAndMembers(t, ctx, db, "history", "a@example.com", "b@example.com", "c@example.com", "d@example.com")
		for _, round := range []int{0, 2} {
			if _, err := db.ExecContext(ctx, `INSERT INTO rounds (organization, id, scheduled_date, done) VALUES ('history', $1, CURRENT_TIMESTAMP, TRUE)`, round); err != nil {
				t.Fatal(err)
			}
		}
		for _, pair := range []struct {
			id1, id2 string
			extra    any
			round    int
		}{
			{id1: "a@example.com", id2: "b@example.com", extra: nil, round: 0},
			{id1: "c@example.com", id2: "d@example.com", extra: "", round: 0},
			{id1: "a@example.com", id2: "d@example.com", extra: nil, round: 2},
		} {
			if _, err := db.ExecContext(ctx, `
INSERT INTO pairs (organization, id1, id2, extraid, round) VALUES ('history', $1, $2, $3, $4)
`, pair.id1, pair.id2, pair.extra, pair.round); err != nil {
				t.Fatal(err)
			}
		}
		grouped, err := repository.PairHistory(ctx, "history")
		if err != nil {
			t.Fatal(err)
		}
		if len(grouped) != 1 || len(grouped[0]) != 2 {
			t.Fatalf("PairHistory() = %#v, want only round zero", grouped)
		}
		if grouped[0][0].ExtraID != nil {
			t.Fatalf("NULL ExtraID = %#v", grouped[0][0].ExtraID)
		}
		if grouped[0][1].ExtraID == nil || *grouped[0][1].ExtraID != "" {
			t.Fatalf("empty ExtraID = %#v", grouped[0][1].ExtraID)
		}
	})
}

func TestSaveRoundPostgreSQLRollsBackEveryStage(t *testing.T) {
	for _, stage := range []string{"pair", "history", "done"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			withIsolatedIntegrationDB(t, func(ctx context.Context, db *sql.DB) {
				repository := migratedStore(t, ctx, db)
				seedOrganizationAndMembers(t, ctx, db, "save", "a@example.com", "b@example.com")
				if _, err := db.ExecContext(ctx, `INSERT INTO rounds (organization, id, scheduled_date, done) VALUES ('save', 0, CURRENT_TIMESTAMP, FALSE)`); err != nil {
					t.Fatal(err)
				}
				if stage == "history" {
					installFailureTrigger(t, ctx, db, "members", "fail_member_update", "UPDATE")
				}
				if stage == "done" {
					installFailureTrigger(t, ctx, db, "rounds", "fail_round_update", "UPDATE")
				}

				round := pairing.NewRound(0)
				if stage == "pair" {
					round.AddPair("a@example.com", "missing@example.com")
				} else {
					round.AddPair("a@example.com", "b@example.com")
				}
				members := pairing.MembersMap{
					"a@example.com": {ID: "a@example.com", LastRoundWith: map[string]int{"b@example.com": 0}},
					"b@example.com": {ID: "b@example.com", LastRoundWith: map[string]int{"a@example.com": 0}},
				}
				if err := repository.SaveRound(ctx, "save", round, members); err == nil {
					t.Fatalf("SaveRound() succeeded at %s stage", stage)
				}
				var pairCount int
				var done bool
				var nonEmptyHistories int
				if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pairs WHERE organization = 'save'`).Scan(&pairCount); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRowContext(ctx, `SELECT done FROM rounds WHERE organization = 'save' AND id = 0`).Scan(&done); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRowContext(ctx, `SELECT count(*) FROM members WHERE organization = 'save' AND last_round_with <> '{}'::jsonb`).Scan(&nonEmptyHistories); err != nil {
					t.Fatal(err)
				}
				if pairCount != 0 || done || nonEmptyHistories != 0 {
					t.Fatalf("rollback state: pairs=%d done=%t histories=%d", pairCount, done, nonEmptyHistories)
				}
			})
		})
	}
}

func TestSaveRoundPostgreSQLCommitsPairsHistoriesAndCompletion(t *testing.T) {
	withIsolatedIntegrationDB(t, func(ctx context.Context, db *sql.DB) {
		repository := migratedStore(t, ctx, db)
		seedOrganizationAndMembers(t, ctx, db, "save-success", "a@example.com", "b@example.com")
		if _, err := db.ExecContext(ctx, `INSERT INTO rounds (organization, id, scheduled_date, done) VALUES ('save-success', 0, CURRENT_TIMESTAMP, FALSE)`); err != nil {
			t.Fatal(err)
		}
		round := pairing.NewRound(0)
		round.AddPair("a@example.com", "b@example.com")
		members := pairing.MembersMap{
			"a@example.com": {ID: "a@example.com", LastRoundWith: map[string]int{"b@example.com": 0}},
			"b@example.com": {ID: "b@example.com", LastRoundWith: map[string]int{"a@example.com": 0}},
		}
		if err := repository.SaveRound(ctx, "save-success", round, members); err != nil {
			t.Fatal(err)
		}
		var pairCount, updatedHistories int
		var done bool
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pairs WHERE organization = 'save-success'`).Scan(&pairCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM members WHERE organization = 'save-success' AND last_round_with <> '{}'::jsonb`).Scan(&updatedHistories); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT done FROM rounds WHERE organization = 'save-success' AND id = 0`).Scan(&done); err != nil {
			t.Fatal(err)
		}
		if pairCount != 1 || updatedHistories != 2 || !done {
			t.Fatalf("saved state: pairs=%d histories=%d done=%t", pairCount, updatedHistories, done)
		}
	})
}

func TestReconstructPairHistoryPostgreSQLSymmetricAndRepeatable(t *testing.T) {
	withIsolatedIntegrationDB(t, func(ctx context.Context, db *sql.DB) {
		repository := migratedStore(t, ctx, db)
		seedOrganizationAndMembers(t, ctx, db, "reconstruct", "a@example.com", "b@example.com", "c@example.com")
		if _, err := db.ExecContext(ctx, `UPDATE members SET last_round_with = '{"stale@example.com":99}'::jsonb WHERE organization = 'reconstruct'`); err != nil {
			t.Fatal(err)
		}
		for _, round := range []int{0, 1} {
			if _, err := db.ExecContext(ctx, `INSERT INTO rounds (organization, id, scheduled_date, done) VALUES ('reconstruct', $1, CURRENT_TIMESTAMP, TRUE)`, round); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.ExecContext(ctx, `
INSERT INTO pairs (organization, id1, id2, extraid, round) VALUES
('reconstruct', 'a@example.com', 'b@example.com', NULL, 0),
('reconstruct', 'a@example.com', 'b@example.com', 'c@example.com', 1)
`); err != nil {
			t.Fatal(err)
		}

		first, err := repository.ReconstructPairHistory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !first.Completed || first.Organizations != 1 || first.Members != 3 || first.Pairs != 2 {
			t.Fatalf("first reconstruction result = %+v", first)
		}
		firstHistories := readHistories(t, ctx, db, "reconstruct")
		want := map[string]string{
			"a@example.com": `{"b@example.com": 1, "c@example.com": 1}`,
			"b@example.com": `{"a@example.com": 1, "c@example.com": 1}`,
			"c@example.com": `{"a@example.com": 1, "b@example.com": 1}`,
		}
		if !reflect.DeepEqual(firstHistories, want) {
			t.Fatalf("reconstructed histories = %#v, want %#v", firstHistories, want)
		}
		second, err := repository.ReconstructPairHistory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !second.Completed || !reflect.DeepEqual(readHistories(t, ctx, db, "reconstruct"), firstHistories) {
			t.Fatalf("second reconstruction = %+v, histories changed", second)
		}
	})
}

func migratedStore(t *testing.T, ctx context.Context, db *sql.DB) *Store {
	t.Helper()
	migrator, err := NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	repository, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func seedOrganizationAndMembers(t *testing.T, ctx context.Context, db *sql.DB, organization string, emails ...string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO organizations (name, admin) VALUES ($1, 'admin@example.com')`, organization); err != nil {
		t.Fatal(err)
	}
	for _, email := range emails {
		if _, err := db.ExecContext(ctx, `
INSERT INTO members (organization, email, name, metadata, last_round_with, active)
VALUES ($1, $2, $2, '{}'::jsonb, '{}'::jsonb, TRUE)
`, organization, email); err != nil {
			t.Fatal(err)
		}
	}
}

func installFailureTrigger(t *testing.T, ctx context.Context, db *sql.DB, table, trigger, operation string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
CREATE OR REPLACE FUNCTION fail_test_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'injected mutation failure';
END;
$$
`); err != nil {
		t.Fatal(err)
	}
	query := `CREATE TRIGGER ` + trigger + ` BEFORE ` + operation + ` ON ` + table + ` FOR EACH ROW EXECUTE FUNCTION fail_test_mutation()`
	if _, err := db.ExecContext(ctx, query); err != nil {
		t.Fatal(err)
	}
}

func readHistories(t *testing.T, ctx context.Context, db *sql.DB, organization string) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT email, last_round_with::text FROM members WHERE organization = $1 ORDER BY email`, organization)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var email, history string
		if err := rows.Scan(&email, &history); err != nil {
			t.Fatal(err)
		}
		result[email] = history
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
