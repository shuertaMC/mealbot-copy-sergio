package pairing

import (
	"strings"
	"testing"
)

func firstIndex(n int) int { return 0 }

func TestNewPairOrdersIDs(t *testing.T) {
	p := NewPair("b@x.com", "a@x.com")
	if p.ID1 != "a@x.com" || p.ID2 != "b@x.com" || p.ExtraID != "" {
		t.Fatalf("unexpected pair %+v", p)
	}
	if NewPair("a@x.com", "b@x.com") != p {
		t.Fatal("NewPair should be order independent")
	}
}

func TestRoundAddPairTracksPaired(t *testing.T) {
	r := NewRound(3)
	r.AddPair("b", "a")
	if !r.IsPaired("a") || !r.IsPaired("b") || r.IsPaired("c") {
		t.Fatal("IsPaired mismatch")
	}
	if !r.Pairs[NewPair("a", "b")] {
		t.Fatal("pair not recorded")
	}
	if !strings.Contains(r.String(), "Round 3") {
		t.Fatalf("unexpected String(): %q", r.String())
	}
}

func TestAlgorithmEvenPairsEveryone(t *testing.T) {
	members, err := getMockMembersMap(6)
	if err != nil {
		t.Fatal(err)
	}

	updated, round := runPairingAlgorithm(members, 0, firstIndex)

	if round.Number != 0 {
		t.Fatalf("round number = %d", round.Number)
	}
	if len(round.Pairs) != 3 {
		t.Fatalf("expected 3 pairs, got %d", len(round.Pairs))
	}
	for id := range members {
		if !round.IsPaired(id) {
			t.Errorf("%s was not paired", id)
		}
	}
	// every pair records round 0 as the last round together
	for pair := range round.Pairs {
		if updated[pair.ID1].LastRoundWith[pair.ID2] != 0 || updated[pair.ID2].LastRoundWith[pair.ID1] != 0 {
			t.Errorf("last round with not updated for %v", pair)
		}
	}
}

func TestAlgorithmOddHoldsOutOneMember(t *testing.T) {
	members, err := getMockMembersMap(5)
	if err != nil {
		t.Fatal(err)
	}

	_, round := runPairingAlgorithm(members, 1, firstIndex)

	if len(round.Pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(round.Pairs))
	}
	if len(round.Paired) != 4 {
		t.Fatalf("expected 4 paired members, got %d", len(round.Paired))
	}
}

func TestAlgorithmAvoidsRecentRepeatsWhenPossible(t *testing.T) {
	members, err := getMockMembersMap(4)
	if err != nil {
		t.Fatal(err)
	}
	// a and b matched in the previous round; everything else is fresh
	members["a@gmail.com"].LastRoundWith["b@gmail.com"] = 4
	members["b@gmail.com"].LastRoundWith["a@gmail.com"] = 4

	for i := 0; i < 20; i++ {
		_, round := runPairingAlgorithm(members, 5, func(n int) int { return n - 1 })
		if round.Pairs[NewPair("a@gmail.com", "b@gmail.com")] {
			t.Fatal("a and b were re-paired although alternatives existed")
		}
	}
}

func TestAlgorithmDoesNotMutateInput(t *testing.T) {
	members, _ := getMockMembersMap(4)
	runPairingAlgorithm(members, 0, firstIndex)
	for _, m := range members {
		for other, last := range m.LastRoundWith {
			if last != -1 {
				t.Fatalf("input mutated: %s with %s = %d", m.ID, other, last)
			}
		}
	}
}
