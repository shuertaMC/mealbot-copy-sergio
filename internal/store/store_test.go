package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/johnamadeo/mealbot/internal/store"
	"github.com/johnamadeo/mealbot/internal/testutil"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	return store.New(testutil.NewDB(t))
}

func TestOrganizations(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	if err := st.CreateOrganization(ctx, "", "admin@x.com"); err != store.ErrEmptyOrganizationName {
		t.Errorf("empty name: got %v", err)
	}
	if err := st.CreateOrganization(ctx, "ysc", "admin@x.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateOrganization(ctx, "ysc", "admin@x.com"); err == nil {
		t.Error("expected duplicate organization error")
	}
	if err := st.CreateOrganization(ctx, "other", "someone@x.com"); err != nil {
		t.Fatal(err)
	}

	orgs, err := st.GetOrganizations(ctx, "admin@x.com")
	if err != nil || !reflect.DeepEqual(orgs, []string{"ysc"}) {
		t.Errorf("orgs = %v, %v", orgs, err)
	}
	if orgs, _ := st.GetOrganizations(ctx, "nobody@x.com"); len(orgs) != 0 || orgs == nil {
		t.Errorf("expected empty non-nil list, got %#v", orgs)
	}
	all, err := st.ListOrganizations(ctx)
	if err != nil || !reflect.DeepEqual(all, []string{"other", "ysc"}) {
		t.Errorf("all = %v, %v", all, err)
	}

	if trait, err := st.GetCrossMatchTrait(ctx, "ysc"); err != nil || trait != "" {
		t.Errorf("unset trait = %q, %v", trait, err)
	}
	if trait, err := st.GetCrossMatchTrait(ctx, "missing"); err != nil || trait != "" {
		t.Errorf("unknown org trait = %q, %v", trait, err)
	}
	if err := st.SetCrossMatchTrait(ctx, "ysc", "college"); err != nil {
		t.Fatal(err)
	}
	if trait, _ := st.GetCrossMatchTrait(ctx, "ysc"); trait != "college" {
		t.Errorf("trait = %q", trait)
	}
}

func TestMembersJSONBRoundTripAndSync(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	if err := st.CreateOrganization(ctx, "ysc", "admin@x.com"); err != nil {
		t.Fatal(err)
	}

	first := []store.Member{
		{Email: "ann@x.com", Name: "Ann", Metadata: map[string]string{"college": "SM", "note": "ünï \"quoted\""}},
		{Email: "bob@x.com", Name: "Bob", Metadata: map[string]string{"college": "JE"}},
	}
	if err := st.SaveMembers(ctx, "ysc", first); err != nil {
		t.Fatal(err)
	}

	members, err := st.GetMembers(ctx, "ysc", true)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %+v, %v", members, err)
	}
	if members[0].Name != "Ann" || members[0].Metadata["note"] != "ünï \"quoted\"" {
		t.Errorf("metadata did not round trip: %+v", members[0])
	}
	if !reflect.DeepEqual(members[0].LastRoundWith, map[string]int{"bob@x.com": -1}) {
		t.Errorf("last round with = %v", members[0].LastRoundWith)
	}

	// record some history, then re-upload with Bob renamed, Ann dropped, Cy added
	if err := st.UpdateLastRoundWith(ctx, "ysc", map[string]map[string]int{
		"ann@x.com": {"bob@x.com": 3},
		"bob@x.com": {"ann@x.com": 3},
	}); err != nil {
		t.Fatal(err)
	}
	second := []store.Member{
		{Email: "bob@x.com", Name: "Robert", Metadata: map[string]string{"college": "JE"}},
		{Email: "cy@x.com", Name: "Cy", Metadata: map[string]string{"college": "SM"}},
	}
	if err := st.SaveMembers(ctx, "ysc", second); err != nil {
		t.Fatal(err)
	}

	active, _ := st.GetMembers(ctx, "ysc", true)
	if len(active) != 2 || active[0].Name != "Cy" || active[1].Name != "Robert" {
		t.Fatalf("active members = %+v", active)
	}
	everyone, _ := st.GetMembers(ctx, "ysc", false)
	if len(everyone) != 3 {
		t.Errorf("inactive members must be kept, got %d members", len(everyone))
	}
	bob := active[1]
	if bob.LastRoundWith["ann@x.com"] != 3 || bob.LastRoundWith["cy@x.com"] != -1 {
		t.Errorf("bob history = %v", bob.LastRoundWith)
	}

	// re-adding Ann reactivates her with her history intact
	if err := st.SaveMembers(ctx, "ysc", append(second, first[0])); err != nil {
		t.Fatal(err)
	}
	active, _ = st.GetMembers(ctx, "ysc", true)
	if len(active) != 3 {
		t.Errorf("expected 3 active members, got %d", len(active))
	}
}

func TestRoundsLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	if err := st.CreateOrganization(ctx, "ysc", "admin@x.com"); err != nil {
		t.Fatal(err)
	}

	for _, date := range []string{"2019-01-02 10:55:00", "2019-01-03 10:55:00", "2019-01-04 10:55:00"} {
		if err := st.AddRound(ctx, "ysc", date); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddRound(ctx, "ysc", "not a date"); err == nil {
		t.Error("expected an error for an invalid date")
	}
	if err := st.AddRound(ctx, "missing-org", "2019-01-02 10:55:00"); err == nil {
		t.Error("expected a foreign key error for an unknown organization")
	}

	rounds, err := st.GetRounds(ctx, "ysc")
	if err != nil || len(rounds) != 3 || rounds[0][:19] != "2019-01-02 10:55:00" {
		t.Fatalf("rounds = %v, %v", rounds, err)
	}

	if err := st.RescheduleRound(ctx, "ysc", "2020-05-06 07:08:09", 1); err != nil {
		t.Fatal(err)
	}
	rounds, _ = st.GetRounds(ctx, "ysc")
	if rounds[1][:19] != "2020-05-06 07:08:09" {
		t.Errorf("rounds = %v", rounds)
	}

	// removing the first round shifts the others down
	if err := st.RemoveRound(ctx, "ysc", 0); err != nil {
		t.Fatal(err)
	}
	rounds, _ = st.GetRounds(ctx, "ysc")
	if len(rounds) != 2 || rounds[0][:19] != "2020-05-06 07:08:09" || rounds[1][:19] != "2019-01-04 10:55:00" {
		t.Errorf("rounds after removal = %v", rounds)
	}

	due, err := st.DueRounds(ctx)
	if err != nil || len(due) != 2 || due[0] != (store.RoundRef{Organization: "ysc", ID: 0}) {
		t.Errorf("due = %v, %v", due, err)
	}
}

func TestSaveRoundAndGetPairs(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	if err := st.CreateOrganization(ctx, "ysc", "admin@x.com"); err != nil {
		t.Fatal(err)
	}
	members := []store.Member{
		{Email: "ann@x.com", Name: "Ann", Metadata: map[string]string{}},
		{Email: "bob@x.com", Name: "Bob", Metadata: map[string]string{}},
	}
	if err := st.SaveMembers(ctx, "ysc", members); err != nil {
		t.Fatal(err)
	}
	if err := st.AddRound(ctx, "ysc", "2019-01-02 10:55:00"); err != nil {
		t.Fatal(err)
	}

	if pairs, err := st.GetPairs(ctx, "ysc"); err != nil || len(pairs) != 0 || pairs == nil {
		t.Fatalf("expected empty non-nil pairs, got %#v, %v", pairs, err)
	}

	err := st.SaveRound(ctx, "ysc", 0,
		[]store.PairRecord{{ID1: "ann@x.com", ID2: "bob@x.com"}},
		map[string]map[string]int{
			"ann@x.com": {"bob@x.com": 0},
			"bob@x.com": {"ann@x.com": 0},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	pairs, err := st.GetPairs(ctx, "ysc")
	want := [][]store.PairRecord{{{ID1: "ann@x.com", ID2: "bob@x.com"}}}
	if err != nil || !reflect.DeepEqual(pairs, want) {
		t.Errorf("pairs = %v, %v", pairs, err)
	}

	got, _ := st.GetMembers(ctx, "ysc", true)
	if got[0].LastRoundWith["bob@x.com"] != 0 {
		t.Errorf("history not saved: %v", got[0].LastRoundWith)
	}
	if due, _ := st.DueRounds(ctx); len(due) != 0 {
		t.Errorf("round should be done, due = %v", due)
	}

	// a failing pair rolls the whole round back
	err = st.SaveRound(ctx, "ysc", 0, []store.PairRecord{{ID1: "nobody@x.com", ID2: "bob@x.com"}}, nil)
	if err == nil {
		t.Error("expected foreign key error")
	}
}
