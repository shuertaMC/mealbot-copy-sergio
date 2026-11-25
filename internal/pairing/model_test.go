package pairing

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPairAndRoundConstruction(t *testing.T) {
	pair := NewPair("z@example.com", "a@example.com")
	if pair != (Pair{ID1: "a@example.com", ID2: "z@example.com"}) {
		t.Fatalf("NewPair() = %#v", pair)
	}
	if got, want := pair.String(), fmt.Sprintf("%-40s %-40s %-40s", "a@example.com", "z@example.com", ""); got != want {
		t.Fatalf("Pair.String() = %q, want %q", got, want)
	}
	round := NewRound(7)
	round.AddPair("z@example.com", "a@example.com")
	if round.Number != 7 || !round.IsPaired("a@example.com") || !round.IsPaired("z@example.com") {
		t.Fatalf("round state = %#v", round)
	}
	if len(round.Pairs) != 1 || len(round.DrawOrderedPairs) != 1 || !round.Pairs[pair] {
		t.Fatalf("round pairs = %#v", round)
	}
	if got := round.String(); !strings.Contains(got, "Round 7") || !strings.Contains(got, pair.String()) {
		t.Fatalf("Round.String() = %q", got)
	}
}

func TestMembersMapStringIsDeterministic(t *testing.T) {
	members := MembersMap{
		"b": {ID: "b", LastRoundWith: map[string]int{"c": 2, "a": 1}},
		"a": {ID: "a", LastRoundWith: map[string]int{"b": 3}},
	}
	want := "X's last round with Y\n" +
		"a\n\tb                              : 3\n\n" +
		"b\n\ta                              : 1\n\tc                              : 2\n\n"
	if got := members.String(); got != want {
		t.Fatalf("MembersMap.String() = %q, want %q", got, want)
	}
}

func TestUpdateLastRoundWithIsSymmetricAndInitializesMaps(t *testing.T) {
	members := MembersMap{"a": {ID: "a"}, "b": {ID: "b"}}
	members.UpdateLastRoundWith("a", "b", 4)
	if members["a"].LastRoundWith["b"] != 4 || members["b"].LastRoundWith["a"] != 4 {
		t.Fatalf("history = %#v", members)
	}
	before := members.clone()
	members.UpdateLastRoundWith("a", "missing", 9)
	if !reflect.DeepEqual(members, before) {
		t.Fatalf("missing-member update mutated history: %#v", members)
	}
}

func TestDatabaseBoundaryConversions(t *testing.T) {
	records := []MemberRecord{{
		ID: "a", Name: "A", Metadata: map[string]string{"team": "red"}, LastRoundWith: map[string]int{"b": 2},
	}}
	members := MembersFromRecords(records, "team")
	if got := members["a"]; got.ID != "a" || got.Name != "A" || got.Trait != "red" || got.LastRoundWith["b"] != 2 {
		t.Fatalf("MembersFromRecords() = %#v", members)
	}
	records[0].LastRoundWith["b"] = 8
	if members["a"].LastRoundWith["b"] != 2 {
		t.Fatal("MembersFromRecords did not copy history")
	}
	round := NewRound(6)
	round.AddPair("z", "a")
	if got, want := PairRecords("org", round), []PairRecord{{Organization: "org", ID1: "a", ID2: "z", Round: 6}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PairRecords() = %#v, want %#v", got, want)
	}
}
