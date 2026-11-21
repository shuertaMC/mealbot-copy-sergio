package store

import (
	"context"
	"testing"

	"github.com/johnamadeo/mealbot/internal/testutil/pgtest"
)

func TestStoreAgainstSchema(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	st := New(pool)

	// cross match trait: missing org and unset trait both give ""
	if trait, err := st.GetCrossMatchTrait(ctx, "nope"); err != nil || trait != "" {
		t.Fatalf("trait = %q, %v", trait, err)
	}

	if err := st.CreateOrganization(ctx, "", "a"); err == nil {
		t.Fatal("empty organization name must be rejected")
	}
	if err := st.CreateOrganization(ctx, "org", "admin"); err != nil {
		t.Fatal(err)
	}
	err := st.CreateOrganization(ctx, "org", "admin")
	if !IsDuplicateKeyErr(err) {
		t.Fatalf("expected duplicate key error, got %v", err)
	}
	if trait, err := st.GetCrossMatchTrait(ctx, "org"); err != nil || trait != "" {
		t.Fatalf("trait = %q, %v", trait, err)
	}
	if err := st.SetCrossMatchTrait(ctx, "org", "year"); err != nil {
		t.Fatal(err)
	}
	if trait, _ := st.GetCrossMatchTrait(ctx, "org"); trait != "year" {
		t.Fatalf("trait = %q", trait)
	}

	// rounds: first id is 0, ids are compacted after removal
	for _, d := range []string{"2019-01-01 10:00:00", "2019-01-02 10:00:00", "2019-01-03 10:00:00"} {
		if err := st.AddRound(ctx, "org", d); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RemoveRound(ctx, "org", 0); err != nil {
		t.Fatal(err)
	}
	rounds, err := st.GetRounds(ctx, "org")
	if err != nil || len(rounds) != 2 {
		t.Fatalf("rounds = %v, %v", rounds, err)
	}

	// members: JSONB round trip, deactivation, re-adding keeps -1 history
	m := func(email string) Member {
		return Member{Organization: "org", Email: email, Name: "N " + email, Metadata: map[string]string{"year": "2019"}}
	}
	if err := st.SaveMembers(ctx, "org", []Member{m("a@x.com"), m("b@x.com")}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetMembers(ctx, "org", true)
	if err != nil || len(got) != 2 {
		t.Fatalf("members = %+v, %v", got, err)
	}
	if got[0].Metadata["year"] != "2019" || got[0].LastRoundWith["b@x.com"] != -1 {
		t.Fatalf("jsonb round trip failed: %+v", got[0])
	}

	if err := st.SaveMembers(ctx, "org", []Member{m("a@x.com"), m("c@x.com")}); err != nil {
		t.Fatal(err)
	}
	active, _ := st.GetMembers(ctx, "org", true)
	all, _ := st.GetMembers(ctx, "org", false)
	if len(active) != 2 || len(all) != 3 {
		t.Fatalf("active=%d all=%d", len(active), len(all))
	}
	for _, mem := range active {
		if mem.Email == "a@x.com" && mem.LastRoundWith["c@x.com"] != -1 {
			t.Fatalf("a should have -1 with new member c: %+v", mem.LastRoundWith)
		}
	}

	// pairs and last_round_with updates
	if err := st.InsertPair(ctx, "org", "a@x.com", "c@x.com", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateLastRoundWith(ctx, "org", "a@x.com", map[string]int{"c@x.com": 0}); err != nil {
		t.Fatal(err)
	}
	pairs, err := st.GetPairs(ctx, "org")
	if err != nil || len(pairs) != 1 || len(pairs[0]) != 1 {
		t.Fatalf("pairs = %+v, %v", pairs, err)
	}
	if pairs[0][0].Member1.Email != "a@x.com" || pairs[0][0].Member2.Email != "c@x.com" {
		t.Fatalf("pair members = %+v", pairs[0][0])
	}

	pending, err := st.GetPendingRounds(ctx)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if err := st.MarkRoundDone(ctx, "org", 0); err != nil {
		t.Fatal(err)
	}
	pending, _ = st.GetPendingRounds(ctx)
	if len(pending) != 1 {
		t.Fatalf("pending after done = %+v", pending)
	}

	orgs, err := st.GetOrganizations(ctx, "admin")
	if err != nil || len(orgs) != 1 {
		t.Fatalf("orgs = %v, %v", orgs, err)
	}
	all2, err := st.GetAllOrganizations(ctx)
	if err != nil || len(all2) != 1 {
		t.Fatalf("all orgs = %v, %v", all2, err)
	}
}
