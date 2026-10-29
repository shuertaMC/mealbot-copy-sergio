// Package pairing implements the algorithm that matches members for a round,
// and the runner that loads members, sends emails and stores the result.
package pairing

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
)

const (
	// MaxTries is the max. number of times to attempt re-matching if a non-optimal matching is initially made
	MaxTries = 50
	// RecentRoundRange : max. no. of rounds between 2 pairings that is considered recent
	RecentRoundRange = 5
)

// Pair :
type Pair struct {
	ID1     string
	ID2     string
	ExtraID string
}

// NewPair creates a pair with the ids in lexicographic order.
func NewPair(name1 string, name2 string) Pair {
	if name1 < name2 {
		return Pair{
			ID1:     name1,
			ID2:     name2,
			ExtraID: "",
		}
	}
	return Pair{
		ID1:     name2,
		ID2:     name1,
		ExtraID: "",
	}
}

func (p Pair) String() string {
	return fmt.Sprintf("%-40s %-40s %-40s", p.ID1, p.ID2, p.ExtraID)
}

// Round : Data structure for storing info on one round of pairings between members
type Round struct {
	Number           int
	Pairs            map[Pair]bool
	Paired           map[string]bool
	DrawOrderedPairs []Pair // for logging; keeps order in which pairs were made
	RecentMatches    int    // number of pairs that were matched again within RecentRoundRange
}

// NewRound :
func NewRound(roundNumber int) Round {
	return Round{
		Number:           roundNumber,
		Pairs:            map[Pair]bool{},
		Paired:           map[string]bool{},
		DrawOrderedPairs: []Pair{},
	}
}

// AddPair : Record a new pairing made during the round
func (r *Round) AddPair(member1 string, member2 string) {
	r.Paired[member1] = true
	r.Paired[member2] = true

	pair := NewPair(member1, member2)
	r.Pairs[pair] = true

	r.DrawOrderedPairs = append(r.DrawOrderedPairs, pair)
}

// IsPaired : Check if member has already been paired this round
func (r Round) IsPaired(member string) bool {
	_, ok := r.Paired[member]
	return ok
}

func (r Round) String() string {
	header := fmt.Sprintf("---------\nRound %d\n---------\n", r.Number)

	pairs := []string{}
	for _, pair := range r.DrawOrderedPairs {
		pairs = append(pairs, pair.String())
	}

	return header + strings.Join(pairs, "\n") + "\n"
}

// MinimalMember : A pared down version of the store's Member struct for the pairing process
type MinimalMember struct {
	ID            string
	Name          string
	Trait         string
	LastRoundWith map[string]int
}

// MembersMap maps a member id (email) to the member.
type MembersMap map[string]MinimalMember

// UpdateLastRoundWith : Updates the last round 2 members were matched with each other
func (mm *MembersMap) UpdateLastRoundWith(id1 string, id2 string, roundNum int) {
	(*mm)[id1].LastRoundWith[id2] = roundNum
	(*mm)[id2].LastRoundWith[id1] = roundNum
}

func (mm MembersMap) String() string {
	ids := make([]string, 0, len(mm))
	for memberID := range mm {
		ids = append(ids, memberID)
	}
	sort.Strings(ids)

	str := "X's last round with Y\n"
	for _, memberID := range ids {
		str += fmt.Sprintf("%s\n", memberID)

		for partnerID, lastRound := range mm[memberID].LastRoundWith {
			str += fmt.Sprintf("\t%-30s : %d\n", partnerID, lastRound)
		}
		str += "\n"
	}
	return str
}

// Clone : Deep copy of the members map (including each member's LastRoundWith)
func (mm MembersMap) Clone() MembersMap {
	out := make(MembersMap, len(mm))
	for id, member := range mm {
		lastRoundWith := make(map[string]int, len(member.LastRoundWith))
		for partnerID, lastRound := range member.LastRoundWith {
			lastRoundWith[partnerID] = lastRound
		}
		member.LastRoundWith = lastRoundWith
		out[id] = member
	}
	return out
}

// Run pairs up members for one round and returns the members with their
// LastRoundWith updated, plus the round. The input is not modified.
//
// All randomness comes from rng so that callers (and tests) can inject a
// seeded generator. Members and candidates are visited in sorted order so
// that a seeded rng yields a reproducible round.
func Run(members MembersMap, roundNum int, rng *rand.Rand) (MembersMap, Round) {
	memberIds := make([]string, 0, len(members))
	for id := range members {
		memberIds = append(memberIds, id)
	}
	sort.Strings(memberIds)

	tries := 0
	var round Round
	var tempMembers MembersMap
	// retry until a) a round w/o repeats is found, or b) MaxTries is reached
	for {
		tempMembers = members.Clone()

		round = NewRound(roundNum)
		numRecentMatches := 0

		// hold out odd member out
		var extraMemberID string
		if len(tempMembers)%2 == 1 {
			extraMemberID = memberIds[rng.IntN(len(memberIds))]
		}

		for _, memberID := range memberIds {
			if round.IsPaired(memberID) || memberID == extraMemberID {
				continue
			}

			member := tempMembers[memberID]

			candidateIDs := make([]string, 0, len(member.LastRoundWith))
			for candidateID := range member.LastRoundWith {
				candidateIDs = append(candidateIDs, candidateID)
			}
			sort.Strings(candidateIDs)

			var goodCandidates, okCandidates, badCandidates []MinimalMember
			for _, candidateID := range candidateIDs {
				// history may mention members that are no longer active
				candidate, known := members[candidateID]
				if !known || candidateID == memberID {
					continue
				}
				if round.IsPaired(candidateID) || candidateID == extraMemberID {
					continue
				}

				lastRound := member.LastRoundWith[candidateID]
				notRecentlyMatched := lastRound == -1 || round.Number-lastRound > RecentRoundRange
				// NOTE: the name is misleading; this has always been true when the traits are EQUAL
				hasDifferentTraits := member.Trait == candidate.Trait

				switch {
				case notRecentlyMatched && hasDifferentTraits:
					goodCandidates = append(goodCandidates, candidate)
				case notRecentlyMatched && !hasDifferentTraits:
					okCandidates = append(okCandidates, candidate)
				default:
					badCandidates = append(badCandidates, candidate)
				}
			}

			var pool []MinimalMember
			switch {
			case len(goodCandidates) > 0:
				pool = goodCandidates
			case len(okCandidates) > 0:
				pool = okCandidates
			case len(badCandidates) > 0:
				pool = badCandidates
				numRecentMatches++
			default:
				// nobody left to pair with
				continue
			}

			partner := pool[rng.IntN(len(pool))]
			round.AddPair(memberID, partner.ID)
			tempMembers.UpdateLastRoundWith(memberID, partner.ID, round.Number)
		}

		tries++
		round.RecentMatches = numRecentMatches

		if numRecentMatches == 0 || tries == MaxTries {
			break
		}
	}

	return tempMembers, round
}
