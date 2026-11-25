package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestOrganizationAndMemberRepositoriesPostgreSQL(t *testing.T) {
	withIsolatedIntegrationDB(t, func(ctx context.Context, db *sql.DB) {
		migrator, err := NewMigrator(db)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrator.Apply(ctx); err != nil {
			t.Fatal(err)
		}
		store, err := New(db)
		if err != nil {
			t.Fatal(err)
		}

		if err := store.CreateOrganization(ctx, "", "admin@example.com"); !errors.Is(err, ErrOrganizationNameRequired) {
			t.Fatalf("empty organization error = %v", err)
		}
		if err := store.CreateOrganization(ctx, "invalid", ""); err == nil {
			t.Fatal("empty administrator succeeded, want database constraint error")
		}
		if err := store.CreateOrganization(ctx, " \t", " \t"); err != nil {
			t.Fatalf("create whitespace-only organization: %v", err)
		}
		whitespaceOrganizations, err := store.OrganizationsByAdmin(ctx, " \t")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{" \t"}; !reflect.DeepEqual(whitespaceOrganizations, want) {
			t.Fatalf("whitespace organizations = %q, want %q", whitespaceOrganizations, want)
		}
		for _, organization := range []Organization{
			{Name: "members", Admin: "admin@example.com"},
			{Name: "alpha", Admin: "admin@example.com"},
			{Name: "other", Admin: "other@example.com"},
		} {
			if err := store.CreateOrganization(ctx, organization.Name, organization.Admin); err != nil {
				t.Fatal(err)
			}
		}
		organizations, err := store.OrganizationsByAdmin(ctx, "admin@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"alpha", "members"}; !reflect.DeepEqual(organizations, want) {
			t.Fatalf("organizations = %v, want %v", organizations, want)
		}
		trait, err := store.CrossMatchTrait(ctx, "members")
		if err != nil || trait != nil {
			t.Fatalf("initial trait = %v, error = %v, want nil", trait, err)
		}
		cohort := "cohort"
		if err := store.SetCrossMatchTrait(ctx, "members", &cohort); err != nil {
			t.Fatal(err)
		}
		trait, err = store.CrossMatchTrait(ctx, "members")
		if err != nil || trait == nil || *trait != cohort {
			t.Fatalf("configured trait = %v, error = %v", trait, err)
		}
		if err := store.SetCrossMatchTrait(ctx, "members", nil); err != nil {
			t.Fatal(err)
		}
		trait, err = store.CrossMatchTrait(ctx, "members")
		if err != nil || trait != nil {
			t.Fatalf("cleared trait = %v, error = %v, want nil", trait, err)
		}
		trait, err = store.CrossMatchTrait(ctx, "missing")
		if err != nil || trait != nil {
			t.Fatalf("missing organization trait = %v, error = %v, want nil", trait, err)
		}

		initialCSV := strings.NewReader("NAME,Email,Cohort,Note\n Alice ,alice@example.com,blue,\"unicode ✓ and comma, retained\"\nBob,bob@example.com,red,plain\n")
		if _, err := store.SynchronizeMembersCSV(ctx, "members", initialCSV); err != nil {
			t.Fatal(err)
		}
		members, err := store.Members(ctx, "members", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(members) != 2 || members[0].Name != "Alice" || members[1].Name != "Bob" {
			t.Fatalf("initial ordered members = %#v", members)
		}
		if got := members[0].Metadata["note"]; got != "unicode ✓ and comma, retained" {
			t.Fatalf("JSONB metadata round trip = %q", got)
		}
		if members[0].LastRoundWith["bob@example.com"] != -1 || members[1].LastRoundWith["alice@example.com"] != -1 {
			t.Fatalf("initial history is not symmetric: %#v", members)
		}

		if _, err := db.ExecContext(ctx, `
UPDATE members
SET last_round_with = CASE email
    WHEN 'alice@example.com' THEN '{"bob@example.com": 3}'::jsonb
    ELSE '{"alice@example.com": 3}'::jsonb
END
WHERE organization = 'members'
`); err != nil {
			t.Fatal(err)
		}
		updatedPlan, err := PlanMemberSynchronization("members", []MemberInput{
			{Name: "Alice Updated", Email: "alice@example.com", Metadata: map[string]string{"cohort": "green", "note": "replaced"}},
			{Name: "Carol", Email: "carol@example.com", Metadata: map[string]string{"cohort": "blue", "json": `{"nested":true}`}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SynchronizeMembers(ctx, updatedPlan); err != nil {
			t.Fatal(err)
		}
		all, err := store.Members(ctx, "members", false)
		if err != nil {
			t.Fatal(err)
		}
		byEmail := membersByEmail(all)
		if byEmail["alice@example.com"].Name != "Alice Updated" || byEmail["alice@example.com"].Metadata["cohort"] != "green" {
			t.Fatalf("present member was not updated: %#v", byEmail["alice@example.com"])
		}
		if byEmail["bob@example.com"].Active {
			t.Fatalf("omitted member remained active: %#v", byEmail["bob@example.com"])
		}
		if !byEmail["carol@example.com"].Active {
			t.Fatalf("new member is inactive: %#v", byEmail["carol@example.com"])
		}
		for _, pair := range [][3]any{
			{"alice@example.com", "bob@example.com", 3},
			{"bob@example.com", "alice@example.com", 3},
			{"alice@example.com", "carol@example.com", -1},
			{"carol@example.com", "alice@example.com", -1},
			{"bob@example.com", "carol@example.com", -1},
			{"carol@example.com", "bob@example.com", -1},
		} {
			if got := byEmail[pair[0].(string)].LastRoundWith[pair[1].(string)]; got != pair[2].(int) {
				t.Errorf("history %s -> %s = %d, want %d", pair[0], pair[1], got, pair[2])
			}
		}

		reactivate, err := PlanMemberSynchronization("members", []MemberInput{
			{Name: "Bob Reactivated", Email: "bob@example.com", Metadata: map[string]string{"cohort": "red"}},
			{Name: "Carol", Email: "carol@example.com", Metadata: map[string]string{"cohort": "blue"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SynchronizeMembers(ctx, reactivate); err != nil {
			t.Fatal(err)
		}
		all, err = store.Members(ctx, "members", false)
		if err != nil {
			t.Fatal(err)
		}
		byEmail = membersByEmail(all)
		if !byEmail["bob@example.com"].Active || byEmail["bob@example.com"].Name != "Bob Reactivated" {
			t.Fatalf("member was not reactivated: %#v", byEmail["bob@example.com"])
		}
		if byEmail["alice@example.com"].Active {
			t.Fatalf("newly omitted member remained active: %#v", byEmail["alice@example.com"])
		}
		if byEmail["bob@example.com"].LastRoundWith["alice@example.com"] != 3 {
			t.Fatalf("reactivation lost history: %#v", byEmail["bob@example.com"])
		}

		if err := store.SetCrossMatchTrait(ctx, "members", &cohort); err != nil {
			t.Fatal(err)
		}
		pairingMembers, err := store.LoadMembers(ctx, "members")
		if err != nil {
			t.Fatal(err)
		}
		if len(pairingMembers) != 2 || pairingMembers["bob@example.com"].Trait != "red" || pairingMembers["carol@example.com"].Trait != "blue" {
			t.Fatalf("pairing member boundary = %#v", pairingMembers)
		}
	})
}

func TestSynchronizeMembersPostgreSQLRollsBackPartialInsert(t *testing.T) {
	withIsolatedIntegrationDB(t, func(ctx context.Context, db *sql.DB) {
		migrator, err := NewMigrator(db)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrator.Apply(ctx); err != nil {
			t.Fatal(err)
		}
		store, err := New(db)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateOrganization(ctx, "rollback", "admin@example.com"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `ALTER TABLE members ADD CONSTRAINT reject_test_name CHECK (name <> 'Reject')`); err != nil {
			t.Fatal(err)
		}
		plan, err := PlanMemberSynchronization("rollback", []MemberInput{
			{Name: "Accepted", Email: "a@example.com"},
			{Name: "Reject", Email: "z@example.com"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SynchronizeMembers(ctx, plan); err == nil {
			t.Fatal("SynchronizeMembers() succeeded, want constraint failure")
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM members WHERE organization = 'rollback'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("partial synchronization committed %d rows", count)
		}
	})
}

func membersByEmail(members []Member) map[string]Member {
	result := make(map[string]Member, len(members))
	for _, member := range members {
		result[member.Email] = member
	}
	return result
}
