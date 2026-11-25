package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var memberColumns = []string{"organization", "email", "name", "metadata", "last_round_with", "active"}

func memberDriverRow(email, name, metadata, history string, active bool) []driver.Value {
	return []driver.Value{"org", email, name, []byte(metadata), []byte(history), active}
}

func TestInitializeSymmetricRelationshipsRetainsHistory(t *testing.T) {
	t.Parallel()
	members := map[string]Member{
		"a@example.com": {Email: "a@example.com", LastRoundWith: map[string]int{"b@example.com": 4}},
		"b@example.com": {Email: "b@example.com", LastRoundWith: map[string]int{}},
		"c@example.com": {Email: "c@example.com"},
	}
	initializeSymmetricRelationships(members)
	want := map[string]map[string]int{
		"a@example.com": {"b@example.com": 4, "c@example.com": -1},
		"b@example.com": {"a@example.com": 4, "c@example.com": -1},
		"c@example.com": {"a@example.com": -1, "b@example.com": -1},
	}
	for email, history := range want {
		if !reflect.DeepEqual(members[email].LastRoundWith, history) {
			t.Errorf("history for %s = %#v, want %#v", email, members[email].LastRoundWith, history)
		}
	}
}

func TestScanMembersPropagatesScanAndIterationErrors(t *testing.T) {
	t.Parallel()
	scanErr := errors.New("scan failed")
	rows := &scanErrorRows{err: scanErr}
	if _, err := scanMembers(rows); !errors.Is(err, scanErr) {
		t.Fatalf("scan error = %v, want %v", err, scanErr)
	}
	iterationErr := errors.New("iteration failed")
	if _, err := scanMembers(&emptyErrorRows{err: iterationErr}); !errors.Is(err, iterationErr) {
		t.Fatalf("iteration error = %v, want %v", err, iterationErr)
	}
}

type scanErrorRows struct{ err error }

func (r *scanErrorRows) Next() bool        { return true }
func (r *scanErrorRows) Scan(...any) error { return r.err }
func (r *scanErrorRows) Err() error        { return nil }

type emptyErrorRows struct{ err error }

func (r *emptyErrorRows) Next() bool        { return false }
func (r *emptyErrorRows) Scan(...any) error { return nil }
func (r *emptyErrorRows) Err() error        { return r.err }

func TestSynchronizeMembersRejectsInvalidatedPlanBeforeTransaction(t *testing.T) {
	state := &driverState{}
	db := openDriverDB(state)
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	plan := MemberSyncPlan{Organization: "org", Members: []MemberInput{{Name: "Alice", Email: ""}}}
	if err := store.SynchronizeMembers(context.Background(), plan); err == nil {
		t.Fatal("SynchronizeMembers() succeeded, want validation error")
	}
	if events := state.snapshot(); len(events) != 0 {
		t.Fatalf("database touched before plan validation: %v", events)
	}
}

func TestSynchronizeMembersCommitsAtomicChanges(t *testing.T) {
	state := &driverState{
		queryColumns: memberColumns,
		queryValues: [][]driver.Value{
			memberDriverRow("old@example.com", "Old", `{}`, `{}`, true),
		},
	}
	db := openDriverDB(state)
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanMemberSynchronization("org", []MemberInput{{Name: "New", Email: "new@example.com", Metadata: map[string]string{"team": "blue"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SynchronizeMembers(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	events := strings.Join(state.snapshot(), "\n")
	for _, required := range []string{"begin", "FOR UPDATE", "INSERT INTO members", "UPDATE members", "commit"} {
		if !strings.Contains(events, required) {
			t.Errorf("events missing %q:\n%s", required, events)
		}
	}
	if strings.Contains(events, "rollback") {
		t.Fatalf("successful sync rolled back:\n%s", events)
	}
}

func TestSynchronizeMembersRollsBackAtEveryDatabaseFailureStage(t *testing.T) {
	tests := []struct {
		name       string
		failSQL    string
		rows       [][]driver.Value
		members    []MemberInput
		wantPhrase string
	}{
		{name: "load", failSQL: "FROM members", members: []MemberInput{{Name: "A", Email: "a@example.com"}}, wantPhrase: "load current members"},
		{name: "insert", failSQL: "INSERT INTO members", members: []MemberInput{{Name: "A", Email: "a@example.com"}}, wantPhrase: "insert member"},
		{name: "update present", failSQL: "UPDATE members", rows: [][]driver.Value{memberDriverRow("a@example.com", "Old", `{}`, `{}`, true)}, members: []MemberInput{{Name: "A", Email: "a@example.com"}}, wantPhrase: "update member"},
		{name: "deactivate omitted", failSQL: "UPDATE members", rows: [][]driver.Value{memberDriverRow("old@example.com", "Old", `{}`, `{}`, true)}, members: []MemberInput{{Name: "A", Email: "a@example.com"}}, wantPhrase: "deactivate member"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			state := &driverState{failSQL: test.failSQL, queryColumns: memberColumns, queryValues: test.rows}
			db := openDriverDB(state)
			defer db.Close()
			store, err := New(db)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PlanMemberSynchronization("org", test.members)
			if err != nil {
				t.Fatal(err)
			}
			err = store.SynchronizeMembers(context.Background(), plan)
			if err == nil || !strings.Contains(err.Error(), test.wantPhrase) {
				t.Fatalf("error = %v, want phrase %q", err, test.wantPhrase)
			}
			events := state.snapshot()
			if len(events) == 0 || events[len(events)-1] != "rollback" {
				t.Fatalf("events = %v, want final rollback", events)
			}
		})
	}
}
