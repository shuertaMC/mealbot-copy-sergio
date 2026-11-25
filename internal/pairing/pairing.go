// Package pairing contains the pure pairing engine: the Pair, Round and
// MembersMap types and RunPairingAlgorithm. It performs no database or email
// I/O (see package rounds for that), and takes its randomness as a parameter so
// that it can be tested deterministically.
package pairing

import (
	"encoding/json"
	"fmt"
	"math/rand"
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

// NewPair :
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

// MinimalMember : A pared down version of the members.Member struct for the pairing process
type MinimalMember struct {
	ID            string
	Name          string
	Trait         string
	LastRoundWith map[string]int
}

// MembersMap :
type MembersMap map[string]MinimalMember

// UpdateLastRoundWith : Updates the last round 2 members were matched with each other
func (mm *MembersMap) UpdateLastRoundWith(id1 string, id2 string, roundNum int) {
	(*mm)[id1].LastRoundWith[id2] = roundNum
	(*mm)[id2].LastRoundWith[id1] = roundNum
}

func (mm MembersMap) String() string {
	str := "X's last round with Y\n"
	for memberID := range mm {
		str += fmt.Sprintf("%s\n", memberID)

		for partnerID, lastRound := range mm[memberID].LastRoundWith {
			str += fmt.Sprintf("\t%-30s : %d\n", partnerID, lastRound)
		}
		str += "\n"
	}
	return str
}

// RandomIntGenerator returns a random int in [0, n). It matches the signature
// of math/rand.Intn, which is the production default.
type RandomIntGenerator func(int) int

// DefaultRandomIntGenerator is the production random source.
var DefaultRandomIntGenerator RandomIntGenerator = rand.Intn

// NewSeededRandomIntGenerator returns a deterministic generator for the seed,
// intended for tests.
func NewSeededRandomIntGenerator(seed int64) RandomIntGenerator {
	return rand.New(rand.NewSource(seed)).Intn
}

// RunPairingAlgorithm pairs up members for round roundNum and returns the
// updated members (with LastRoundWith refreshed) and the resulting Round.
// genRandomInt is the injectable random source; nil selects
// DefaultRandomIntGenerator.
func RunPairingAlgorithm(members MembersMap, roundNum int, genRandomInt RandomIntGenerator) (MembersMap, Round) {
	if genRandomInt == nil {
		genRandomInt = DefaultRandomIntGenerator
	}

	memberIds := []string{}
	for id := range members {
		memberIds = append(memberIds, id)
	}

	tries := 0
	var round Round
	// retry until a) a round w/o repeats is found, or b) MaxTries is reached
	for {
		// Make a deep copy of the members map
		bytes, _ := json.Marshal(members)
		var tempMembers MembersMap
		json.Unmarshal(bytes, &tempMembers)

		round = NewRound(roundNum)
		numRecentMatches := 0

		// hold out odd member out and add it back in at the end of round
		var extraMemberID string
		if len(tempMembers)%2 == 1 {
			extraMemberID = memberIds[genRandomInt(len(memberIds))]
		}

		for _, memberID := range memberIds {
			if round.IsPaired(memberID) || memberID == extraMemberID {
				continue
			}

			member := tempMembers[memberID]

			var goodCandidates, okCandidates, badCandidates []MinimalMember
			for candidateID, lastRound := range member.LastRoundWith {
				if round.IsPaired(candidateID) || candidateID == extraMemberID {
					continue
				}

				notRecentlyMatched := lastRound == -1 || round.Number-lastRound > RecentRoundRange
				hasDifferentTraits := member.Trait == members[candidateID].Trait

				switch {
				case notRecentlyMatched && hasDifferentTraits:
					goodCandidates = append(goodCandidates, members[candidateID])
				case notRecentlyMatched && !hasDifferentTraits:
					okCandidates = append(okCandidates, members[candidateID])
				default:
					badCandidates = append(badCandidates, members[candidateID])
				}
			}

			var partner MinimalMember
			switch {
			case len(goodCandidates) > 0:
				partner = selectRandomPartner(genRandomInt, goodCandidates)
			case len(okCandidates) > 0:
				partner = selectRandomPartner(genRandomInt, okCandidates)
			default:
				partner = selectRandomPartner(genRandomInt, badCandidates)
				numRecentMatches++
			}

			round.AddPair(memberID, partner.ID)
			tempMembers.UpdateLastRoundWith(memberID, partner.ID, round.Number)
		}

		tries++

		if numRecentMatches == 0 || tries == MaxTries {
			members = tempMembers
			fmt.Println(round)
			fmt.Printf("%d recent matches\n", numRecentMatches)
			break
		}
	}

	return members, round
}

func selectRandomPartner(genRandomInt RandomIntGenerator, members []MinimalMember) MinimalMember {
	return members[genRandomInt(len(members))]
}
