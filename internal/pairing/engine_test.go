package pairing

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/johnamadeo/mealbot/internal/domain"
)

func TestRunPopulationPolicies(t *testing.T) {
	tests := []struct {
		name       string
		population int
		groups     int
		triples    int
	}{
		{name: "empty", population: 0, groups: 0, triples: 0},
		{name: "singleton", population: 1, groups: 0, triples: 0},
		{name: "pair", population: 2, groups: 1, triples: 0},
		{name: "odd triple", population: 3, groups: 1, triples: 1},
		{name: "even", population: 6, groups: 3, triples: 0},
		{name: "odd", population: 7, groups: 3, triples: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := memberMap(test.population)
			before := cloneForTest(input)
			updated, round, err := Run(input, 8, func(int) int { return 0 })
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !reflect.DeepEqual(input, before) {
				t.Fatalf("Run mutated input: got %#v want %#v", input, before)
			}
			if len(round.DrawOrderedPairs) != test.groups {
				t.Fatalf("groups = %d, want %d", len(round.DrawOrderedPairs), test.groups)
			}
			seen := make(map[string]bool)
			triples := 0
			for _, group := range round.DrawOrderedPairs {
				if group.ExtraID != "" {
					triples++
				}
				for _, id := range group.Members() {
					if seen[id] {
						t.Fatalf("member %q appeared more than once", id)
					}
					seen[id] = true
				}
			}
			if triples != test.triples {
				t.Fatalf("triples = %d, want %d", triples, test.triples)
			}
			wantSeen := test.population
			if test.population == 1 {
				wantSeen = 0
			}
			if len(seen) != wantSeen || len(updated) != test.population {
				t.Fatalf("seen %d updated %d, want %d members retained", len(seen), len(updated), test.population)
			}
		})
	}
}

func TestRunPairOrderingAndDrawSequence(t *testing.T) {
	members := memberMap(5)
	var sizes []int
	selector := func(size int) int {
		sizes = append(sizes, size)
		return size - 1
	}
	updated, round, err := Run(members, 10, selector)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []Pair{
		{ID1: "a@example.test", ID2: "d@example.test"},
		{ID1: "b@example.test", ID2: "c@example.test", ExtraID: "e@example.test"},
	}
	if !reflect.DeepEqual(round.DrawOrderedPairs, want) {
		t.Fatalf("draw order = %#v, want %#v", round.DrawOrderedPairs, want)
	}
	if !reflect.DeepEqual(sizes, []int{5, 3, 1, 2}) {
		t.Fatalf("selector sizes = %v, want [5 3 1 2]", sizes)
	}
	for _, group := range round.DrawOrderedPairs {
		ids := group.Members()
		for left := range ids {
			for right := left + 1; right < len(ids); right++ {
				if updated[ids[left]].LastRoundWith[ids[right]] != 10 || updated[ids[right]].LastRoundWith[ids[left]] != 10 {
					t.Fatalf("history for %q/%q is not symmetric: %#v", ids[left], ids[right], updated)
				}
			}
		}
	}
}

func TestRunDeterministicAcrossMapInsertionOrder(t *testing.T) {
	forward := memberMap(4)
	reverse := MembersMap{}
	for _, id := range []string{"d@example.test", "c@example.test", "b@example.test", "a@example.test"} {
		reverse[id] = forward[id]
	}
	sequence := []int{1, 0}
	selector := func() RandomIntGenerator {
		index := 0
		return func(size int) int {
			value := sequence[index] % size
			index++
			return value
		}
	}
	firstMembers, firstRound, err := Run(forward, 4, selector())
	if err != nil {
		t.Fatal(err)
	}
	secondMembers, secondRound, err := Run(reverse, 4, selector())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstRound.DrawOrderedPairs, secondRound.DrawOrderedPairs) || !reflect.DeepEqual(firstMembers, secondMembers) {
		t.Fatalf("map order changed result: %#v / %#v", firstRound, secondRound)
	}
}

func TestRunAvoidsRecentAndPrefersDifferentTrait(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(MembersMap)
		want    Pair
	}{
		{
			name: "recent avoided",
			prepare: func(members MembersMap) {
				setHistory(members, "a@example.test", "b@example.test", 8)
			},
			want: Pair{ID1: "a@example.test", ID2: "c@example.test"},
		},
		{
			name: "different trait preferred",
			prepare: func(members MembersMap) {
				member := members["c@example.test"]
				member.Trait = "blue"
				members[member.ID] = member
			},
			want: Pair{ID1: "a@example.test", ID2: "c@example.test"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			members := memberMap(4)
			test.prepare(members)
			_, round, err := Run(members, 10, func(int) int { return 0 })
			if err != nil {
				t.Fatal(err)
			}
			if round.DrawOrderedPairs[0] != test.want {
				t.Fatalf("first pair = %#v, want %#v", round.DrawOrderedPairs[0], test.want)
			}
		})
	}
}

func TestRunMaxTriesFallback(t *testing.T) {
	members := memberMap(2)
	setHistory(members, "a@example.test", "b@example.test", 9)
	calls := 0
	updated, round, err := Run(members, 10, func(int) int { calls++; return 0 })
	if err != nil {
		t.Fatal(err)
	}
	if calls != MaxTries {
		t.Fatalf("selector calls = %d, want %d", calls, MaxTries)
	}
	if len(round.DrawOrderedPairs) != 1 || updated["a@example.test"].LastRoundWith["b@example.test"] != 10 {
		t.Fatalf("fallback result = %#v, %#v", round, updated)
	}
}

func TestRunRejectsInvalidSelectorWithoutMutation(t *testing.T) {
	for _, value := range []int{-1, 1} {
		t.Run(fmt.Sprintf("index_%d", value), func(t *testing.T) {
			members := memberMap(2)
			before := cloneForTest(members)
			returned, _, err := Run(members, 2, func(int) int { return value })
			if !errors.Is(err, ErrInvalidRandomIndex) {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(members, before) || !reflect.DeepEqual(returned, before) {
				t.Fatalf("failure mutated members: input %#v returned %#v", members, returned)
			}
		})
	}
}

func TestEngineValidationAndFormatting(t *testing.T) {
	constructors := []struct {
		name      string
		construct func() (Pair, error)
		want      Pair
	}{
		{name: "pair orders", construct: func() (Pair, error) { return NewPair("z", "a") }, want: Pair{ID1: "a", ID2: "z"}},
		{name: "pair trims", construct: func() (Pair, error) { return NewPair(" z ", " a ") }, want: Pair{ID1: "a", ID2: "z"}},
		{name: "triple orders", construct: func() (Pair, error) { return NewTriple("c", "a", "b") }, want: Pair{ID1: "a", ID2: "b", ExtraID: "c"}},
		{name: "triple trims", construct: func() (Pair, error) { return NewTriple(" c ", " a ", " b ") }, want: Pair{ID1: "a", ID2: "b", ExtraID: "c"}},
	}
	for _, test := range constructors {
		t.Run(test.name, func(t *testing.T) {
			group, err := test.construct()
			if err != nil || group != test.want {
				t.Fatalf("constructor = %#v, %v; want %#v", group, err, test.want)
			}
		})
	}

	invalid := []struct {
		name      string
		construct func() (Pair, error)
	}{
		{name: "blank pair", construct: func() (Pair, error) { return NewPair(" ", "b") }},
		{name: "duplicate pair", construct: func() (Pair, error) { return NewPair("a", "a") }},
		{name: "blank triple", construct: func() (Pair, error) { return NewTriple("a", "b", " ") }},
		{name: "duplicate triple", construct: func() (Pair, error) { return NewTriple("a", "a", "b") }},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.construct()
			if !errors.Is(err, ErrInvalidMembers) || !domain.IsValidation(err) {
				t.Fatalf("constructor error = %v, want pairing and domain validation classifications", err)
			}
		})
	}

	round := NewRound(3)
	if err := round.AddPair("z", "a"); err != nil {
		t.Fatal(err)
	}
	pairText := "a" + strings.Repeat(" ", 40) + "z" + strings.Repeat(" ", 80)
	wantRound := "---------\nRound 3\n---------\n" + pairText + "\n"
	if !round.IsPaired("a") || round.String() != wantRound {
		t.Fatalf("round formatting = %q, want %q", round.String(), wantRound)
	}
	before := round
	if err := round.AddPair("a", " a "); !errors.Is(err, ErrInvalidMembers) || !reflect.DeepEqual(round, before) {
		t.Fatalf("invalid AddPair error = %v, round = %#v; want unchanged %#v", err, round, before)
	}
	members := memberMap(2)
	setHistory(members, "a@example.test", "b@example.test", 1)
	formatted := members.String()
	if strings.Index(formatted, "a@example.test") > strings.Index(formatted, "b@example.test") || !strings.Contains(formatted, ": 1") {
		t.Fatalf("member formatting = %q", formatted)
	}
	if _, _, err := Run(members, -1, func(int) int { return 0 }); !errors.Is(err, ErrInvalidMembers) {
		t.Fatalf("negative round error = %v", err)
	}
	if _, _, err := Run(members, 1, nil); !errors.Is(err, ErrInvalidRandomIndex) {
		t.Fatalf("nil selector error = %v", err)
	}
	bad := MembersMap{"a": {ID: "b"}}
	if _, _, err := Run(bad, 1, func(int) int { return 0 }); !errors.Is(err, ErrInvalidMembers) {
		t.Fatalf("bad member error = %v", err)
	}
}

func TestRunRejectsNonCanonicalMemberIDsBeforeSelection(t *testing.T) {
	tests := []MembersMap{
		{" a@example.test ": {ID: " a@example.test ", LastRoundWith: map[string]int{}}},
		{"a@example.test": {ID: " a@example.test ", LastRoundWith: map[string]int{}}},
	}
	for index, members := range tests {
		t.Run(fmt.Sprintf("case_%d", index), func(t *testing.T) {
			before := cloneForTestUnchecked(members)
			selectorCalled := false
			returned, _, err := Run(members, 1, func(int) int {
				selectorCalled = true
				return 0
			})
			if !errors.Is(err, ErrInvalidMembers) {
				t.Fatalf("Run error = %v, want ErrInvalidMembers", err)
			}
			if selectorCalled || returned != nil || !reflect.DeepEqual(members, before) {
				t.Fatalf("invalid input was used or mutated: called=%t returned=%#v members=%#v", selectorCalled, returned, members)
			}
		})
	}
}

func memberMap(count int) MembersMap {
	members := make(MembersMap, count)
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("%c@example.test", 'a'+index)
		members[id] = MinimalMember{ID: id, Trait: "red", LastRoundWith: map[string]int{}}
	}
	return members
}

func setHistory(members MembersMap, firstID, secondID string, round int) {
	members.UpdateLastRoundWith(firstID, secondID, round)
}

func cloneForTest(members MembersMap) MembersMap {
	clone, err := cloneMembers(members)
	if err != nil {
		panic(err)
	}
	return clone
}

func cloneForTestUnchecked(members MembersMap) MembersMap {
	clone := make(MembersMap, len(members))
	for id, member := range members {
		member.LastRoundWith = cloneHistory(member.LastRoundWith)
		clone[id] = member
	}
	return clone
}
