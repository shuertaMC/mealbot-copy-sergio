package pairing

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func newTestRand(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed+1))
}

func getMockMembersMap(t *testing.T, numMembers int) MembersMap {
	t.Helper()
	alphabet := "abcdefghijklmnopqrstuvwxyz"
	if numMembers > len(alphabet) {
		t.Fatal("Cannot mock more than 26 members")
	}

	members := MembersMap{}
	for i := 0; i < numMembers; i++ {
		letter := alphabet[i : i+1]
		email := letter + "@gmail.com"
		members[email] = MinimalMember{
			ID:            email,
			Name:          "Person " + strings.ToUpper(letter),
			Trait:         "1995",
			LastRoundWith: map[string]int{},
		}

		for j := 0; j < numMembers; j++ {
			if i == j {
				continue
			}
			otherEmail := alphabet[j:j+1] + "@gmail.com"
			members[email].LastRoundWith[otherEmail] = -1
		}
	}
	return members
}

func TestPairingEvenMembersAllPaired(t *testing.T) {
	members := getMockMembersMap(t, 6)
	updated, round := Run(members, 0, newTestRand(1))

	if len(round.Pairs) != 3 || len(round.DrawOrderedPairs) != 3 {
		t.Fatalf("expected 3 pairs, got %d", len(round.Pairs))
	}
	if len(round.Paired) != 6 {
		t.Fatalf("expected all 6 members paired, got %d", len(round.Paired))
	}
	for pair := range round.Pairs {
		if pair.ID1 == pair.ID2 {
			t.Errorf("member paired with themselves: %v", pair)
		}
		if updated[pair.ID1].LastRoundWith[pair.ID2] != 0 || updated[pair.ID2].LastRoundWith[pair.ID1] != 0 {
			t.Errorf("last round with not updated for %v", pair)
		}
	}
}

func TestPairingOddMembersHoldsOneOut(t *testing.T) {
	members := getMockMembersMap(t, 5)
	_, round := Run(members, 3, newTestRand(7))

	if len(round.Pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(round.Pairs))
	}
	if len(round.Paired) != 4 {
		t.Fatalf("expected 4 paired members, got %d", len(round.Paired))
	}
}

func TestPairingDeterministicForSameSeed(t *testing.T) {
	first := runOnce(t, 42)
	second := runOnce(t, 42)
	if first != second {
		t.Errorf("same seed produced different rounds:\n%s\n%s", first, second)
	}
}

func runOnce(t *testing.T, seed uint64) string {
	t.Helper()
	_, round := Run(getMockMembersMap(t, 8), 0, newTestRand(seed))
	return round.String()
}

func TestPairingDoesNotMutateInput(t *testing.T) {
	members := getMockMembersMap(t, 4)
	Run(members, 2, newTestRand(3))
	for id, member := range members {
		for partner, lastRound := range member.LastRoundWith {
			if lastRound != -1 {
				t.Errorf("input mutated: %s with %s = %d", id, partner, lastRound)
			}
		}
	}
}

func TestPairingAvoidsRecentMatches(t *testing.T) {
	members := getMockMembersMap(t, 4)
	// a and b met last round; any other combination is fine
	members["a@gmail.com"].LastRoundWith["b@gmail.com"] = 4
	members["b@gmail.com"].LastRoundWith["a@gmail.com"] = 4

	for seed := uint64(0); seed < 20; seed++ {
		_, round := Run(members, 5, newTestRand(seed))
		if round.RecentMatches != 0 {
			t.Fatalf("seed %d: expected no recent matches, got %d", seed, round.RecentMatches)
		}
		if round.Pairs[NewPair("a@gmail.com", "b@gmail.com")] {
			t.Fatalf("seed %d: a and b were paired again", seed)
		}
	}
}

func TestPairingReportsRecentMatchesWhenUnavoidable(t *testing.T) {
	members := getMockMembersMap(t, 2)
	members["a@gmail.com"].LastRoundWith["b@gmail.com"] = 1
	members["b@gmail.com"].LastRoundWith["a@gmail.com"] = 1

	_, round := Run(members, 2, newTestRand(1))
	if len(round.Pairs) != 1 || round.RecentMatches != 1 {
		t.Errorf("expected 1 pair with 1 recent match, got %d pairs, %d recent", len(round.Pairs), round.RecentMatches)
	}
}

// Pins current behavior: candidates whose trait is EQUAL are preferred.
func TestPairingPrefersEqualTraitCandidates(t *testing.T) {
	members := getMockMembersMap(t, 4)
	for _, id := range []string{"a@gmail.com", "b@gmail.com"} {
		m := members[id]
		m.Trait = "x"
		members[id] = m
	}
	for _, id := range []string{"c@gmail.com", "d@gmail.com"} {
		m := members[id]
		m.Trait = "y"
		members[id] = m
	}

	for seed := uint64(0); seed < 10; seed++ {
		_, round := Run(members, 0, newTestRand(seed))
		if !round.Pairs[NewPair("a@gmail.com", "b@gmail.com")] || !round.Pairs[NewPair("c@gmail.com", "d@gmail.com")] {
			t.Fatalf("seed %d: unexpected pairs %s", seed, round)
		}
	}
}

func TestPairingEdgeCases(t *testing.T) {
	_, round := Run(MembersMap{}, 0, newTestRand(1))
	if len(round.Pairs) != 0 {
		t.Errorf("no members should yield no pairs")
	}

	_, round = Run(getMockMembersMap(t, 1), 0, newTestRand(1))
	if len(round.Pairs) != 0 {
		t.Errorf("a single member should yield no pairs")
	}

	// a member whose history refers to someone who is no longer active
	members := getMockMembersMap(t, 2)
	members["a@gmail.com"].LastRoundWith["gone@gmail.com"] = -1
	_, round = Run(members, 0, newTestRand(1))
	if len(round.Pairs) != 1 {
		t.Errorf("expected the 2 active members to be paired, got %d pairs", len(round.Pairs))
	}
}

func TestCloneIsDeep(t *testing.T) {
	members := getMockMembersMap(t, 3)
	clone := members.Clone()
	clone.UpdateLastRoundWith("a@gmail.com", "b@gmail.com", 9)

	if members["a@gmail.com"].LastRoundWith["b@gmail.com"] != -1 {
		t.Error("modifying the clone changed the original")
	}
}

func TestPairAndRoundStrings(t *testing.T) {
	if p := NewPair("z", "a"); p.ID1 != "a" || p.ID2 != "z" {
		t.Errorf("NewPair did not order ids: %+v", p)
	}

	round := NewRound(2)
	round.AddPair("b", "a")
	if !round.IsPaired("a") || !round.IsPaired("b") || round.IsPaired("c") {
		t.Error("IsPaired mismatch")
	}
	if s := round.String(); !strings.Contains(s, "Round 2") || !strings.Contains(s, "a") {
		t.Errorf("unexpected round string %q", s)
	}
}
