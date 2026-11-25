package pairing

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getMockMembersMap(numMembers int) (MembersMap, error) {
	alphabet := "abcdefghijklmnopqrstuvwxyz"
	if numMembers > len(alphabet) {
		return MembersMap{}, errors.New("Cannot mock more than 26 members")
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
	return members, nil
}

// assertRoundInvariants checks that every member is paired at most once, that
// Pairs/Paired/DrawOrderedPairs agree, and that exactly wantPairs pairs exist.
func assertRoundInvariants(t *testing.T, round Round, wantPairs int) {
	t.Helper()
	seen := map[string]int{}
	for _, p := range round.DrawOrderedPairs {
		seen[p.ID1]++
		seen[p.ID2]++
		assert.NotEqual(t, p.ID1, p.ID2, "member paired with itself")
		assert.True(t, p.ID1 < p.ID2, "pair is not normalized")
		assert.True(t, round.Pairs[p], "pair missing from Pairs")
	}
	for id, n := range seen {
		assert.Equal(t, 1, n, "member %s paired %d times", id, n)
		assert.True(t, round.IsPaired(id))
	}
	assert.Len(t, round.DrawOrderedPairs, wantPairs)
	assert.Len(t, round.Pairs, wantPairs)
	assert.Len(t, round.Paired, wantPairs*2)
}

func TestNewPair(t *testing.T) {
	tests := []struct {
		name    string
		a, b    string
		wantID1 string
		wantID2 string
	}{
		{"already ordered", "a", "b", "a", "b"},
		{"reversed", "b", "a", "a", "b"},
		{"equal", "x", "x", "x", "x"},
		{"emails", "z@gmail.com", "a@gmail.com", "a@gmail.com", "z@gmail.com"},
		{"empty first", "", "a", "", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPair(tt.a, tt.b)
			assert.Equal(t, tt.wantID1, p.ID1)
			assert.Equal(t, tt.wantID2, p.ID2)
			assert.Equal(t, "", p.ExtraID)
		})
	}
	assert.Equal(t, NewPair("a", "b"), NewPair("b", "a"))
}

func TestPairString(t *testing.T) {
	s := NewPair("a", "b").String()
	assert.Contains(t, s, "a")
	assert.Contains(t, s, "b")
	assert.Len(t, s, 40*3+2)
}

func TestRoundBookkeeping(t *testing.T) {
	r := NewRound(3)
	assert.Equal(t, 3, r.Number)
	assert.Empty(t, r.Pairs)
	assert.Empty(t, r.Paired)
	assert.Empty(t, r.DrawOrderedPairs)
	assert.False(t, r.IsPaired("a"))

	r.AddPair("b", "a")
	r.AddPair("c", "d")

	assert.True(t, r.IsPaired("a"))
	assert.True(t, r.IsPaired("b"))
	assert.True(t, r.IsPaired("c"))
	assert.False(t, r.IsPaired("e"))
	assert.True(t, r.Pairs[NewPair("a", "b")])
	assert.Equal(t, []Pair{NewPair("a", "b"), NewPair("c", "d")}, r.DrawOrderedPairs)

	str := r.String()
	assert.Contains(t, str, "Round 3")
	assert.Contains(t, str, NewPair("a", "b").String())
	assert.Contains(t, str, NewPair("c", "d").String())
}

func TestUpdateLastRoundWith(t *testing.T) {
	tests := []struct {
		name     string
		id1, id2 string
		round    int
	}{
		{"first round", "a@gmail.com", "b@gmail.com", 0},
		{"later round", "a@gmail.com", "c@gmail.com", 7},
		{"reversed args", "c@gmail.com", "a@gmail.com", 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			members, err := getMockMembersMap(3)
			require.NoError(t, err)
			members.UpdateLastRoundWith(tt.id1, tt.id2, tt.round)
			assert.Equal(t, tt.round, members[tt.id1].LastRoundWith[tt.id2])
			assert.Equal(t, tt.round, members[tt.id2].LastRoundWith[tt.id1])
			// the third member is untouched
			for id, m := range members {
				if id == tt.id1 || id == tt.id2 {
					continue
				}
				for _, v := range m.LastRoundWith {
					assert.Equal(t, -1, v)
				}
			}
		})
	}
}

func TestMembersMapString(t *testing.T) {
	members, err := getMockMembersMap(2)
	require.NoError(t, err)
	s := members.String()
	assert.Contains(t, s, "last round with")
	assert.Contains(t, s, "a@gmail.com")
	assert.Contains(t, s, "b@gmail.com")
}

func TestSeededRandomIntGeneratorDeterministic(t *testing.T) {
	g1 := NewSeededRandomIntGenerator(42)
	g2 := NewSeededRandomIntGenerator(42)
	for i := 0; i < 20; i++ {
		a, b := g1(10), g2(10)
		assert.Equal(t, a, b)
		assert.True(t, a >= 0 && a < 10)
	}
}

func TestRunPairingAlgorithmCorrectness(t *testing.T) {
	tests := []struct {
		name      string
		n         int
		wantPairs int
	}{
		{"empty", 0, 0},
		{"single member", 1, 0},
		{"two members", 2, 1},
		{"odd three", 3, 1},
		{"even four", 4, 2},
		{"odd seven", 7, 3},
		{"even ten", 10, 5},
		{"odd eleven", 11, 5},
	}
	for _, tt := range tests {
		for seed := int64(1); seed <= 5; seed++ {
			t.Run(fmt.Sprintf("%s/seed%d", tt.name, seed), func(t *testing.T) {
				members, err := getMockMembersMap(tt.n)
				require.NoError(t, err)

				updated, round := RunPairingAlgorithm(members, 0, NewSeededRandomIntGenerator(seed))

				assert.Equal(t, 0, round.Number)
				assertRoundInvariants(t, round, tt.wantPairs)
				assert.Len(t, updated, tt.n)
				for _, p := range round.DrawOrderedPairs {
					assert.Equal(t, 0, updated[p.ID1].LastRoundWith[p.ID2])
					assert.Equal(t, 0, updated[p.ID2].LastRoundWith[p.ID1])
				}
				// input is not mutated
				for _, m := range members {
					for _, v := range m.LastRoundWith {
						assert.Equal(t, -1, v)
					}
				}
			})
		}
	}
}

func TestRunPairingAlgorithmZeroRandomSource(t *testing.T) {
	members, err := getMockMembersMap(3)
	require.NoError(t, err)
	zero := func(n int) int { return 0 }
	for round := 0; round < 3; round++ {
		var r Round
		members, r = RunPairingAlgorithm(members, round, zero)
		assertRoundInvariants(t, r, 1)
	}
}

func TestRunPairingAlgorithmNilRandomUsesDefault(t *testing.T) {
	members, err := getMockMembersMap(6)
	require.NoError(t, err)
	_, round := RunPairingAlgorithm(members, 0, nil)
	assertRoundInvariants(t, round, 3)
}

func TestRunPairingAlgorithmAvoidsPreviousPairs(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			members, err := getMockMembersMap(4)
			require.NoError(t, err)
			// a-b and c-d were paired in the previous round
			members.UpdateLastRoundWith("a@gmail.com", "b@gmail.com", 5)
			members.UpdateLastRoundWith("c@gmail.com", "d@gmail.com", 5)

			_, round := RunPairingAlgorithm(members, 6, NewSeededRandomIntGenerator(seed))

			assertRoundInvariants(t, round, 2)
			assert.False(t, round.Pairs[NewPair("a@gmail.com", "b@gmail.com")])
			assert.False(t, round.Pairs[NewPair("c@gmail.com", "d@gmail.com")])
		})
	}
}

func TestRunPairingAlgorithmRepeatAllowedWhenUnavoidable(t *testing.T) {
	// With only two members the only possible pair is the recent one.
	members, err := getMockMembersMap(2)
	require.NoError(t, err)
	members.UpdateLastRoundWith("a@gmail.com", "b@gmail.com", 1)

	_, round := RunPairingAlgorithm(members, 2, NewSeededRandomIntGenerator(1))
	assertRoundInvariants(t, round, 1)
	assert.True(t, round.Pairs[NewPair("a@gmail.com", "b@gmail.com")])
}

func TestPairingAlgorithmEndToEndTest(t *testing.T) {
	// Run consecutive rounds, feeding each result into the next. In every
	// round all members are paired, and LastRoundWith reflects the round.
	const n = 6
	const rounds = 3
	for seed := int64(1); seed <= 10; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			members, err := getMockMembersMap(n)
			require.NoError(t, err)
			gen := NewSeededRandomIntGenerator(seed)

			met := map[Pair]int{}
			for roundNum := 0; roundNum < rounds; roundNum++ {
				var round Round
				members, round = RunPairingAlgorithm(members, roundNum, gen)
				assertRoundInvariants(t, round, n/2)
				for p := range round.Pairs {
					met[p]++
					assert.Equal(t, roundNum, members[p.ID1].LastRoundWith[p.ID2])
				}
			}
			assert.Len(t, members, n)

			// The algorithm avoids pairs from the last RecentRoundRange rounds,
			// so within 3 rounds no pair should meet more than once
			// (a repeat-free schedule of 3 rounds among 6 always exists).
			for p, c := range met {
				assert.Equal(t, 1, c, "pair %v met %d times", p, c)
			}
		})
	}
}
