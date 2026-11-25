package pairing

import (
	"reflect"
	"testing"
)

type sequenceRandom struct {
	values []int
	calls  int
}

func (r *sequenceRandom) Intn(n int) int {
	value := 0
	if r.calls < len(r.values) {
		value = r.values[r.calls]
	}
	r.calls++
	return value % n
}

func member(id, trait string, history map[string]int) MinimalMember {
	return MinimalMember{ID: id, Name: id, Trait: trait, LastRoundWith: history}
}

func TestChoosePartnerCandidatePriority(t *testing.T) {
	tests := []struct {
		name    string
		members MembersMap
		want    string
		recent  bool
	}{
		{
			name: "never paired equal trait before all others",
			members: MembersMap{
				"a": member("a", "red", map[string]int{"b": -1, "c": -1, "d": 9}),
				"b": member("b", "red", nil),
				"c": member("c", "blue", nil),
				"d": member("d", "red", nil),
			},
			want: "b",
		},
		{
			name: "more than five rounds old is preferred",
			members: MembersMap{
				"a": member("a", "red", map[string]int{"b": 4, "c": -1}),
				"b": member("b", "red", nil),
				"c": member("c", "blue", nil),
			},
			want: "b",
		},
		{
			name: "exactly five rounds old remains recent",
			members: MembersMap{
				"a": member("a", "red", map[string]int{"b": 5, "c": -1}),
				"b": member("b", "red", nil),
				"c": member("c", "blue", nil),
			},
			want: "c",
		},
		{
			name: "non-recent different trait before repeat",
			members: MembersMap{
				"a": member("a", "red", map[string]int{"b": 9, "c": -1}),
				"b": member("b", "red", nil),
				"c": member("c", "blue", nil),
			},
			want: "c",
		},
		{
			name: "recent repeat when required",
			members: MembersMap{
				"a": member("a", "red", map[string]int{"b": 9, "c": 8}),
				"b": member("b", "red", nil),
				"c": member("c", "blue", nil),
			},
			want:   "b",
			recent: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ids := test.members.IDs()
			got, recent, ok := choosePartner(test.members, NewRound(10), "a", "", ids, RandomFunc(func(int) int { return 0 }))
			if !ok || got != test.want || recent != test.recent {
				t.Fatalf("choosePartner() = %q, %v, %v; want %q, %v, true", got, recent, ok, test.want, test.recent)
			}
		})
	}
}

func TestMatchEvenPopulationAndSymmetricHistory(t *testing.T) {
	members := completeMembers("a", "b", "c", "d")
	updated, round := Match(members, 3, RandomFunc(func(int) int { return 0 }))
	want := []Pair{NewPair("a", "b"), NewPair("c", "d")}
	if !reflect.DeepEqual(round.DrawOrderedPairs, want) {
		t.Fatalf("pairs = %#v, want %#v", round.DrawOrderedPairs, want)
	}
	if len(round.Paired) != 4 {
		t.Fatalf("paired count = %d", len(round.Paired))
	}
	for _, pair := range round.DrawOrderedPairs {
		if updated[pair.ID1].LastRoundWith[pair.ID2] != 3 || updated[pair.ID2].LastRoundWith[pair.ID1] != 3 {
			t.Fatalf("asymmetric history for %#v: %#v", pair, updated)
		}
	}
	if members["a"].LastRoundWith["b"] != -1 {
		t.Fatal("Match mutated input members")
	}
}

func TestMatchOddPopulationUsesRandomHoldoutWithoutExtraID(t *testing.T) {
	members := completeMembers("a", "b", "c")
	random := &sequenceRandom{values: []int{1, 0}}
	updated, round := Match(members, 2, random)
	if got, want := round.DrawOrderedPairs, []Pair{NewPair("a", "c")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pairs = %#v, want %#v", got, want)
	}
	if round.DrawOrderedPairs[0].ExtraID != "" || round.IsPaired("b") {
		t.Fatalf("legacy holdout behavior changed: %#v", round)
	}
	if updated["b"].LastRoundWith["a"] != -1 || updated["b"].LastRoundWith["c"] != -1 {
		t.Fatalf("holdout history changed: %#v", updated["b"])
	}
}

func TestMatchRandomSelectionIsDeterministic(t *testing.T) {
	members := completeMembers("a", "b", "c", "d")
	random := &sequenceRandom{values: []int{1, 0}}
	_, round := Match(members, 1, random)
	if got, want := round.DrawOrderedPairs, []Pair{NewPair("a", "c"), NewPair("b", "d")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pairs = %#v, want %#v", got, want)
	}
}

func TestMatchRetriesRecentPairUntilExhaustion(t *testing.T) {
	members := completeMembers("a", "b")
	members.UpdateLastRoundWith("a", "b", 9)
	random := &sequenceRandom{}
	updated, round := Match(members, 10, random)
	if random.calls != MaxTries {
		t.Fatalf("random calls = %d, want %d", random.calls, MaxTries)
	}
	if got, want := round.DrawOrderedPairs, []Pair{NewPair("a", "b")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pairs = %#v", got)
	}
	if updated["a"].LastRoundWith["b"] != 10 || updated["b"].LastRoundWith["a"] != 10 {
		t.Fatalf("history = %#v", updated)
	}
}

func TestMatchHandlesDegeneratePopulations(t *testing.T) {
	tests := []struct {
		name    string
		members MembersMap
	}{
		{name: "nil", members: nil},
		{name: "empty", members: MembersMap{}},
		{name: "single", members: MembersMap{"a": member("a", "red", nil)}},
		{name: "incomplete history", members: MembersMap{"a": member("a", "red", nil), "b": member("b", "red", nil)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updated, round := Match(test.members, 0, nil)
			if len(test.members) < 2 && (len(round.Pairs) != 0 || len(round.Paired) != 0) {
				t.Fatalf("round = %#v", round)
			}
			if len(updated) != len(test.members) {
				t.Fatalf("updated count = %d, want %d", len(updated), len(test.members))
			}
		})
	}
}

func completeMembers(ids ...string) MembersMap {
	members := make(MembersMap, len(ids))
	for _, id := range ids {
		history := make(map[string]int, len(ids)-1)
		for _, other := range ids {
			if other != id {
				history[other] = -1
			}
		}
		members[id] = member(id, "same", history)
	}
	return members
}
