package pairing

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/johnamadeo/mealbot/internal/domain"
)

const (
	// MaxTries is the maximum number of complete draws attempted before a
	// round containing recent matches is accepted as the fallback.
	MaxTries = 50
	// RecentRoundRange is the number of intervening rounds for which a prior
	// match is considered recent and avoided when an alternative draw exists.
	RecentRoundRange = 5
)

var (
	// ErrInvalidRandomIndex reports a selector result outside [0, n).
	ErrInvalidRandomIndex = errors.New("pairing random selector returned an invalid index")
	// ErrInvalidMembers reports inconsistent member IDs in a MembersMap.
	ErrInvalidMembers = errors.New("pairing members are invalid")
)

// Pair is the shared canonical two- or three-member domain group.
type Pair = domain.Pair

// NewPair validates and returns a two-person group in canonical order.
func NewPair(id1, id2 string) (Pair, error) {
	return canonicalGroup(id1, id2)
}

// NewTriple validates and returns a three-person group in canonical order.
func NewTriple(id1, id2, extraID string) (Pair, error) {
	return canonicalGroup(id1, id2, extraID)
}

func canonicalGroup(id1, id2 string, extraID ...string) (Pair, error) {
	pair, err := domain.NewPair(id1, id2, extraID...)
	if err != nil {
		return Pair{}, fmt.Errorf("%w: %w", ErrInvalidMembers, err)
	}
	return pair, nil
}

// Round records one computed round. DrawOrderedPairs is authoritative for
// delivery and trace order.
type Round struct {
	Number           int
	Paired           map[string]bool
	DrawOrderedPairs []Pair
}

// NewRound returns an empty round with initialized maps and draw order.
func NewRound(roundNumber int) Round {
	return Round{
		Number:           roundNumber,
		Paired:           make(map[string]bool),
		DrawOrderedPairs: make([]Pair, 0),
	}
}

// AddPair records a validated canonical two-person group at the end of draw
// order.
func (r *Round) AddPair(member1, member2 string) error {
	pair, err := NewPair(member1, member2)
	if err != nil {
		return err
	}
	r.add(pair)
	return nil
}

// AddTriple records a canonical triple at the end of draw order.
func (r *Round) AddTriple(member1, member2, extra string) error {
	pair, err := NewTriple(member1, member2, extra)
	if err != nil {
		return err
	}
	r.add(pair)
	return nil
}

func (r *Round) add(pair Pair) {
	for _, id := range pair.Members() {
		r.Paired[id] = true
	}
	r.DrawOrderedPairs = append(r.DrawOrderedPairs, pair)
}

// IsPaired reports whether the member already appears in this round.
func (r Round) IsPaired(member string) bool { return r.Paired[member] }

// String formats the round and its groups in draw order.
func (r Round) String() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "---------\nRound %d\n---------\n", r.Number)
	for _, pair := range r.DrawOrderedPairs {
		builder.WriteString(pair.String())
		builder.WriteByte('\n')
	}
	return builder.String()
}

// MinimalMember contains only state used while drawing pairings.
type MinimalMember struct {
	ID            string
	Trait         string
	LastRoundWith map[string]int
}

// MembersMap indexes minimal members by their ID.
type MembersMap map[string]MinimalMember

// UpdateLastRoundWith records one relationship symmetrically. It is a no-op
// when either ID is absent; Run validates membership before using it.
func (mm *MembersMap) UpdateLastRoundWith(id1, id2 string, roundNum int) {
	first, firstOK := (*mm)[id1]
	second, secondOK := (*mm)[id2]
	if !firstOK || !secondOK {
		return
	}
	if first.LastRoundWith == nil {
		first.LastRoundWith = make(map[string]int)
	}
	if second.LastRoundWith == nil {
		second.LastRoundWith = make(map[string]int)
	}
	first.LastRoundWith[id2] = roundNum
	second.LastRoundWith[id1] = roundNum
	(*mm)[id1] = first
	(*mm)[id2] = second
}

// String formats histories in stable member and partner order.
func (mm MembersMap) String() string {
	var builder strings.Builder
	builder.WriteString("X's last round with Y\n")
	for _, id := range sortedMemberIDs(mm) {
		builder.WriteString(id)
		builder.WriteByte('\n')
		partners := make([]string, 0, len(mm[id].LastRoundWith))
		for partner := range mm[id].LastRoundWith {
			partners = append(partners, partner)
		}
		sort.Strings(partners)
		for _, partner := range partners {
			fmt.Fprintf(&builder, "\t%-30s : %d\n", partner, mm[id].LastRoundWith[partner])
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

// RandomIntGenerator selects an integer in [0, n). Run validates every result
// before indexing and returns ErrInvalidRandomIndex for a bad selector.
type RandomIntGenerator func(n int) int

// Run computes a complete round. Empty and singleton maps return no groups;
// an odd population of at least three returns exactly one triple. Returned
// histories are deep copies and input maps are never mutated, including when
// the selector fails.
func Run(members MembersMap, roundNumber int, randomInt RandomIntGenerator) (MembersMap, Round, error) {
	original, err := cloneMembers(members)
	if err != nil {
		return nil, Round{}, err
	}
	if roundNumber < 0 {
		return original, Round{}, fmt.Errorf("%w: round number must be non-negative", ErrInvalidMembers)
	}
	if randomInt == nil {
		return original, Round{}, fmt.Errorf("%w: selector is nil", ErrInvalidRandomIndex)
	}
	if len(original) < 2 {
		return original, NewRound(roundNumber), nil
	}

	var accepted MembersMap
	var acceptedRound Round
	for attempt := 0; attempt < MaxTries; attempt++ {
		candidateMembers, cloneErr := cloneMembers(original)
		if cloneErr != nil {
			return original, Round{}, cloneErr
		}
		round, recentMatches, drawErr := draw(candidateMembers, roundNumber, randomInt)
		if drawErr != nil {
			return original, Round{}, drawErr
		}
		accepted, acceptedRound = candidateMembers, round
		if recentMatches == 0 {
			break
		}
	}
	return accepted, acceptedRound, nil
}

func draw(members MembersMap, roundNumber int, randomInt RandomIntGenerator) (Round, int, error) {
	ids := sortedMemberIDs(members)
	round := NewRound(roundNumber)
	extraID := ""
	if len(ids) >= 3 && len(ids)%2 == 1 {
		index, err := selectIndex(randomInt, len(ids))
		if err != nil {
			return Round{}, 0, err
		}
		extraID = ids[index]
	}

	recentMatches := 0
	for _, memberID := range ids {
		if memberID == extraID || round.IsPaired(memberID) {
			continue
		}
		available := make([]string, 0)
		for _, candidateID := range ids {
			if candidateID != memberID && candidateID != extraID && !round.IsPaired(candidateID) {
				available = append(available, candidateID)
			}
		}
		partner, recent, err := selectPartner(members, memberID, available, roundNumber, randomInt)
		if err != nil {
			return Round{}, 0, err
		}
		if err := round.AddPair(memberID, partner); err != nil {
			return Round{}, 0, err
		}
		members.UpdateLastRoundWith(memberID, partner, roundNumber)
		if recent {
			recentMatches++
		}
	}

	if extraID != "" {
		groupIndex, recent, err := selectTripleGroup(members, round.DrawOrderedPairs, extraID, roundNumber, randomInt)
		if err != nil {
			return Round{}, 0, err
		}
		oldPair := round.DrawOrderedPairs[groupIndex]
		triple, err := NewTriple(oldPair.ID1, oldPair.ID2, extraID)
		if err != nil {
			return Round{}, 0, err
		}
		round.DrawOrderedPairs[groupIndex] = triple
		round.Paired[extraID] = true
		members.UpdateLastRoundWith(extraID, oldPair.ID1, roundNumber)
		members.UpdateLastRoundWith(extraID, oldPair.ID2, roundNumber)
		recentMatches += recent
	}
	return round, recentMatches, nil
}

func selectPartner(members MembersMap, memberID string, candidates []string, roundNumber int, randomInt RandomIntGenerator) (string, bool, error) {
	if len(candidates) == 0 {
		return "", false, fmt.Errorf("%w: member %q has no partner", ErrInvalidMembers, memberID)
	}
	member := members[memberID]
	buckets := make([][]string, 4)
	for _, candidateID := range candidates {
		recent := wasRecent(lastRound(member.LastRoundWith, candidateID), roundNumber)
		different := member.Trait != members[candidateID].Trait
		bucket := 1
		switch {
		case !recent && different:
			bucket = 0
		case recent && different:
			bucket = 2
		case recent:
			bucket = 3
		}
		buckets[bucket] = append(buckets[bucket], candidateID)
	}
	selectedBucket := len(buckets) - 1
	for bucket, options := range buckets[:len(buckets)-1] {
		if len(options) == 0 {
			continue
		}
		selectedBucket = bucket
		break
	}
	options := buckets[selectedBucket]
	index, err := selectIndex(randomInt, len(options))
	if err != nil {
		return "", false, err
	}
	return options[index], selectedBucket >= 2, nil
}

func selectTripleGroup(members MembersMap, pairs []Pair, extraID string, roundNumber int, randomInt RandomIntGenerator) (int, int, error) {
	if len(pairs) == 0 {
		return 0, 0, fmt.Errorf("%w: triple has no pair to join", ErrInvalidMembers)
	}
	type option struct {
		index  int
		recent int
	}
	buckets := make([][]option, 4)
	extra := members[extraID]
	for index, pair := range pairs {
		recent := 0
		different := false
		for _, id := range pair.Members() {
			if wasRecent(lastRound(extra.LastRoundWith, id), roundNumber) {
				recent++
			}
			if extra.Trait != members[id].Trait {
				different = true
			}
		}
		bucket := 1
		switch {
		case recent == 0 && different:
			bucket = 0
		case recent > 0 && different:
			bucket = 2
		case recent > 0:
			bucket = 3
		}
		buckets[bucket] = append(buckets[bucket], option{index: index, recent: recent})
	}
	options := buckets[len(buckets)-1]
	for _, bucketOptions := range buckets[:len(buckets)-1] {
		if len(bucketOptions) == 0 {
			continue
		}
		options = bucketOptions
		break
	}
	selected, err := selectIndex(randomInt, len(options))
	if err != nil {
		return 0, 0, err
	}
	return options[selected].index, options[selected].recent, nil
}

func wasRecent(lastRound, currentRound int) bool {
	return lastRound >= 0 && currentRound-lastRound <= RecentRoundRange
}

func lastRound(history map[string]int, partner string) int {
	last, exists := history[partner]
	if !exists {
		return -1
	}
	return last
}

func selectIndex(randomInt RandomIntGenerator, size int) (int, error) {
	index := randomInt(size)
	if index < 0 || index >= size {
		return 0, fmt.Errorf("%w: got %d for size %d", ErrInvalidRandomIndex, index, size)
	}
	return index, nil
}

func cloneMembers(source MembersMap) (MembersMap, error) {
	clone := make(MembersMap, len(source))
	for key, member := range source {
		if key == "" || member.ID == "" || strings.TrimSpace(key) != key || strings.TrimSpace(member.ID) != member.ID || member.ID != key {
			return nil, fmt.Errorf("%w: map key %q does not match member ID %q", ErrInvalidMembers, key, member.ID)
		}
		member.LastRoundWith = cloneHistory(member.LastRoundWith)
		clone[key] = member
	}
	return clone, nil
}

func cloneHistory(source map[string]int) map[string]int {
	clone := make(map[string]int, len(source))
	for id, round := range source {
		clone[id] = round
	}
	return clone
}

func sortedMemberIDs(members MembersMap) []string {
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
