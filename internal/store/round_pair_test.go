package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnamadeo/mealbot/internal/pairing"
)

func TestListRoundsUsesLegacyFormatOrderAndPropagatesIterationError(t *testing.T) {
	terminalErr := errors.New("iteration failed")
	state := &driverState{queryFixtures: []queryFixture{{
		contains:    "FROM rounds",
		columns:     []string{"scheduled"},
		values:      [][]driver.Value{{"2026-01-02 03:04:05Z"}},
		terminalErr: terminalErr,
	}}}
	db := openDriverDB(state)
	defer db.Close()
	repository, _ := New(db)
	rounds, err := repository.ListRounds(context.Background(), "org")
	if err == nil || !strings.Contains(err.Error(), terminalErr.Error()) {
		t.Fatalf("ListRounds() error = %v, want iteration error", err)
	}
	if rounds != nil {
		t.Fatalf("ListRounds() rounds = %v, want nil on iteration failure", rounds)
	}
	events := strings.Join(state.snapshot(), "\n")
	if !strings.Contains(events, "to_char(scheduled_date, $1)") || !strings.Contains(events, "ORDER BY id") {
		t.Fatalf("round query does not preserve format/order:\n%s", events)
	}
}

func TestAddRoundAllocatesAndRollsBackInsertFailure(t *testing.T) {
	state := &driverState{
		failSQL: "INSERT INTO rounds",
		queryFixtures: []queryFixture{
			{contains: "SELECT name FROM organizations", columns: []string{"name"}, values: [][]driver.Value{{"org"}}},
			{contains: "SELECT COALESCE(MAX(id)", columns: []string{"id"}, values: [][]driver.Value{{int64(0)}}},
		},
	}
	db := openDriverDB(state)
	defer db.Close()
	repository, _ := New(db)
	if _, err := repository.AddRound(context.Background(), "org", "2026-01-01 00:00:00"); err == nil {
		t.Fatal("AddRound() succeeded, want insert failure")
	}
	assertRollbackWithoutCommit(t, state)
}

func TestRescheduleRoundRollsBackFailure(t *testing.T) {
	state := &driverState{
		failSQL: "SET scheduled_date",
		queryFixtures: []queryFixture{{
			contains: "SELECT name FROM organizations", columns: []string{"name"}, values: [][]driver.Value{{"org"}},
		}},
	}
	db := openDriverDB(state)
	defer db.Close()
	repository, _ := New(db)
	if err := repository.RescheduleRound(context.Background(), "org", "2026-01-01", 2); err == nil {
		t.Fatal("RescheduleRound() succeeded, want update failure")
	}
	assertRollbackWithoutCommit(t, state)
}

func TestDeleteRoundRenumberFailureRollsBack(t *testing.T) {
	state := &driverState{
		failSQL: "UPDATE pairs",
		queryFixtures: []queryFixture{
			{contains: "SELECT name FROM organizations", columns: []string{"name"}, values: [][]driver.Value{{"org"}}},
			{
				contains: "SELECT id, scheduled_date, done",
				columns:  []string{"id", "scheduled_date", "done"},
				values:   [][]driver.Value{{int64(2), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), false}},
			},
		},
	}
	db := openDriverDB(state)
	defer db.Close()
	repository, _ := New(db)
	if err := repository.DeleteRound(context.Background(), "org", 1); err == nil {
		t.Fatal("DeleteRound() succeeded, want renumber failure")
	}
	assertRollbackWithoutCommit(t, state)
	events := strings.Join(state.snapshot(), "\n")
	for _, fragment := range []string{"DELETE FROM pairs", "DELETE FROM rounds", "INSERT INTO rounds", "UPDATE pairs"} {
		if !strings.Contains(events, fragment) {
			t.Errorf("DeleteRound() did not execute %q:\n%s", fragment, events)
		}
	}
}

func TestDueRoundsUsesStrictUTCBoundaryAndStableOrder(t *testing.T) {
	state := &driverState{queryFixtures: []queryFixture{{
		contains: "FROM rounds",
		columns:  []string{"organization", "id"},
		values:   [][]driver.Value{{"alpha", int64(0)}, {"beta", int64(1)}},
	}}}
	db := openDriverDB(state)
	defer db.Close()
	repository, _ := New(db)
	boundary := time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("offset", -7*60*60))
	rounds, err := repository.DueRounds(context.Background(), boundary)
	if err != nil {
		t.Fatal(err)
	}
	want := []pairing.ScheduledRound{{Organization: "alpha", Number: 0}, {Organization: "beta", Number: 1}}
	if !reflect.DeepEqual(rounds, want) {
		t.Fatalf("DueRounds() = %#v, want %#v", rounds, want)
	}
	events := strings.Join(state.snapshot(), "\n")
	if !strings.Contains(events, "scheduled_date < ($1::timestamptz AT TIME ZONE 'UTC')") || !strings.Contains(events, "ORDER BY scheduled_date, organization, id") {
		t.Fatalf("due query does not use strict boundary/stable order:\n%s", events)
	}
	if len(state.queryArgs) != 1 || len(state.queryArgs[0]) != 1 {
		t.Fatalf("query args = %#v", state.queryArgs)
	}
	gotBoundary, ok := state.queryArgs[0][0].Value.(time.Time)
	if !ok || gotBoundary.Location() != time.UTC || !gotBoundary.Equal(boundary) {
		t.Fatalf("boundary argument = %#v, want UTC %v", state.queryArgs[0][0].Value, boundary.UTC())
	}
}

func TestPairHistoryStopsAtGapAndPreservesNullAndEmptyExtraID(t *testing.T) {
	columns := []string{
		"round", "extraid",
		"organization", "email", "name", "metadata", "history", "active",
		"organization", "email", "name", "metadata", "history", "active",
		"organization", "email", "name", "metadata", "history", "active",
	}
	activeMember := func(email, name string) []driver.Value {
		return []driver.Value{"org", email, name, []byte(`{"trait":"active"}`), []byte(`{}`), true}
	}
	unresolvedMember := func() []driver.Value {
		return []driver.Value{nil, nil, nil, nil, nil, nil}
	}
	member := func(round int64, extra driver.Value, first, second, optional []driver.Value) []driver.Value {
		return []driver.Value{
			round, extra,
			first[0], first[1], first[2], first[3], first[4], first[5],
			second[0], second[1], second[2], second[3], second[4], second[5],
			optional[0], optional[1], optional[2], optional[3], optional[4], optional[5],
		}
	}
	state := &driverState{queryFixtures: []queryFixture{{
		contains: "FROM pairs AS p",
		columns:  columns,
		values: [][]driver.Value{
			member(0, nil, activeMember("a@example.com", "A"), activeMember("b@example.com", "B"), unresolvedMember()),
			member(0, "", unresolvedMember(), activeMember("b@example.com", "B"), unresolvedMember()),
			member(0, "missing@example.com", activeMember("a@example.com", "A"), unresolvedMember(), unresolvedMember()),
			member(0, "inactive@example.com", activeMember("a@example.com", "A"), activeMember("b@example.com", "B"), unresolvedMember()),
			member(0, "extra@example.com", activeMember("a@example.com", "A"), activeMember("b@example.com", "B"), activeMember("extra@example.com", "Extra")),
			member(2, nil, activeMember("a@example.com", "A"), activeMember("b@example.com", "B"), unresolvedMember()),
		},
	}}}
	db := openDriverDB(state)
	defer db.Close()
	repository, _ := New(db)
	grouped, err := repository.PairHistory(context.Background(), "org")
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped) != 1 || len(grouped[0]) != 5 {
		t.Fatalf("PairHistory() grouping = %#v, want only five round-zero pairs", grouped)
	}
	if grouped[0][0].ExtraID != nil {
		t.Fatalf("NULL extra ID = %#v, want nil", grouped[0][0].ExtraID)
	}
	if grouped[0][1].ExtraID == nil || *grouped[0][1].ExtraID != "" {
		t.Fatalf("empty extra ID = %#v, want present empty string", grouped[0][1].ExtraID)
	}
	if !reflect.DeepEqual(grouped[0][1].Member1, Member{}) {
		t.Fatalf("inactive first member = %#v, want zero value", grouped[0][1].Member1)
	}
	if !reflect.DeepEqual(grouped[0][2].Member2, Member{}) {
		t.Fatalf("missing second member = %#v, want zero value", grouped[0][2].Member2)
	}
	if grouped[0][2].ExtraMember != nil || grouped[0][3].ExtraMember != nil {
		t.Fatalf("missing/inactive extras = %#v/%#v, want nil", grouped[0][2].ExtraMember, grouped[0][3].ExtraMember)
	}
	if grouped[0][4].ExtraMember == nil || grouped[0][4].ExtraMember.Email != "extra@example.com" || !grouped[0][4].ExtraMember.Active {
		t.Fatalf("active extra member = %#v", grouped[0][4].ExtraMember)
	}
	events := strings.Join(state.snapshot(), "\n")
	if strings.Count(events, "active = TRUE") != 3 || strings.Count(events, "LEFT JOIN members") != 3 {
		t.Fatalf("pair query does not resolve all participants through active left joins:\n%s", events)
	}
	if !strings.Contains(events, "ORDER BY p.round, p.id1, p.id2, p.extraid NULLS FIRST") {
		t.Fatalf("pair query lost stable ordering:\n%s", events)
	}
}

func TestSaveRoundRollsBackAtEveryStage(t *testing.T) {
	for _, test := range []struct {
		name    string
		failSQL string
	}{
		{name: "pair", failSQL: "INSERT INTO pairs"},
		{name: "history", failSQL: "SET last_round_with"},
		{name: "done", failSQL: "SET done = TRUE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &driverState{failSQL: test.failSQL}
			db := openDriverDB(state)
			defer db.Close()
			repository, _ := New(db)
			round := pairing.NewRound(3)
			round.AddPair("a@example.com", "b@example.com")
			members := pairing.MembersMap{
				"a@example.com": {ID: "a@example.com", LastRoundWith: map[string]int{"b@example.com": 3}},
				"b@example.com": {ID: "b@example.com", LastRoundWith: map[string]int{"a@example.com": 3}},
			}
			if err := repository.SaveRound(context.Background(), "org", round, members); err == nil {
				t.Fatalf("SaveRound() succeeded at failing %s stage", test.name)
			}
			assertRollbackWithoutCommit(t, state)
		})
	}
}

func TestReconstructPairHistorySelectsActiveMembersAndContiguousRounds(t *testing.T) {
	state := &driverState{queryFixtures: []queryFixture{
		{
			contains: "FROM members",
			columns:  []string{"organization", "email", "name", "metadata", "last_round_with", "active"},
			values: [][]driver.Value{
				{"org", "a@example.com", "A", []byte(`{}`), []byte(`{"stale@example.com":99}`), true},
				{"org", "b@example.com", "B", []byte(`{}`), []byte(`{"stale@example.com":99}`), true},
			},
		},
		{
			contains: "FROM pairs AS p",
			columns:  []string{"organization", "id1", "id2", "extraid", "round"},
			values:   [][]driver.Value{{"org", "a@example.com", "b@example.com", nil, int64(0)}},
		},
	}}
	db := openDriverDB(state)
	defer db.Close()
	repository, _ := New(db)
	result, err := repository.ReconstructPairHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || result.Organizations != 1 || result.Members != 2 || result.Pairs != 1 {
		t.Fatalf("ReconstructPairHistory() result = %+v", result)
	}
	events := strings.Join(state.snapshot(), "\n")
	if !strings.Contains(events, "FROM members WHERE active = TRUE") {
		t.Fatalf("reconstruction member query does not filter active rows:\n%s", events)
	}
	for _, fragment := range []string{"WITH RECURSIVE contiguous_rounds", "WHERE round = 0", "current.round + 1", "INNER JOIN contiguous_rounds"} {
		if !strings.Contains(events, fragment) {
			t.Fatalf("reconstruction pair query does not enforce %q:\n%s", fragment, events)
		}
	}
	if strings.Count(events, "exec:UPDATE members SET last_round_with") != 2 {
		t.Fatalf("reconstruction updates = %d, want two active members:\n%s", strings.Count(events, "exec:UPDATE members SET last_round_with"), events)
	}
	if !strings.HasSuffix(events, "commit") {
		t.Fatalf("transaction events do not end in commit:\n%s", events)
	}
}

func TestReconstructPairHistoryPairIterationErrorRollsBack(t *testing.T) {
	terminalErr := errors.New("pair iteration failed")
	state := &driverState{queryFixtures: []queryFixture{
		{
			contains: "FROM members",
			columns:  []string{"organization", "email", "name", "metadata", "last_round_with", "active"},
			values:   [][]driver.Value{{"org", "a@example.com", "A", []byte(`{}`), []byte(`{}`), true}},
		},
		{
			contains:    "FROM pairs AS p",
			columns:     []string{"organization", "id1", "id2", "extraid", "round"},
			terminalErr: terminalErr,
		},
	}}
	db := openDriverDB(state)
	defer db.Close()
	repository, _ := New(db)
	result, err := repository.ReconstructPairHistory(context.Background())
	if err == nil || !strings.Contains(err.Error(), terminalErr.Error()) {
		t.Fatalf("ReconstructPairHistory() error = %v, want iteration error", err)
	}
	if result != (HistoryReconstructionResult{}) {
		t.Fatalf("failed reconstruction result = %+v, want zero value", result)
	}
	assertRollbackWithoutCommit(t, state)
}

func TestOrderedRoundPairsUsesDrawOrderThenStableMapOrder(t *testing.T) {
	drawn := pairing.Pair{ID1: "c", ID2: "d"}
	round := pairing.Round{
		Pairs: map[pairing.Pair]bool{
			drawn:                            true,
			pairing.Pair{ID1: "a", ID2: "z"}: true,
			pairing.Pair{ID1: "a", ID2: "b"}: true,
		},
		DrawOrderedPairs: []pairing.Pair{drawn},
	}
	got := orderedRoundPairs(round)
	want := []pairing.Pair{drawn, pairing.Pair{ID1: "a", ID2: "b"}, pairing.Pair{ID1: "a", ID2: "z"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orderedRoundPairs() = %#v, want %#v", got, want)
	}
}

func assertRollbackWithoutCommit(t *testing.T, state *driverState) {
	t.Helper()
	events := state.snapshot()
	if len(events) == 0 || events[len(events)-1] != "rollback" {
		t.Fatalf("transaction events = %v, want final rollback", events)
	}
	for _, event := range events {
		if event == "commit" {
			t.Fatalf("transaction events = %v, unexpected commit", events)
		}
	}
}
