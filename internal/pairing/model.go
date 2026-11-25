package pairing

import (
	"fmt"
	"maps"
	"sort"
	"strings"
)

const (
	// MaxTries is the maximum number of attempts to find a round with no
	// recent repeats.
	MaxTries = 50
	// RecentRoundRange is the number of rounds for which a pairing is recent.
	RecentRoundRange = 5
)

// Pair identifies two paired members and retains the legacy optional third ID.
type Pair struct {
	ID1     string
	ID2     string
	ExtraID string
}

// NewPair returns a pair with its first two IDs in canonical order.
func NewPair(id1, id2 string) Pair {
	if id1 < id2 {
		return Pair{ID1: id1, ID2: id2}
	}
	return Pair{ID1: id2, ID2: id1}
}

func (p Pair) String() string {
	return fmt.Sprintf("%-40s %-40s %-40s", p.ID1, p.ID2, p.ExtraID)
}

// Round records one matching attempt and the order in which pairs were drawn.
type Round struct {
	Number           int
	Pairs            map[Pair]bool
	Paired           map[string]bool
	DrawOrderedPairs []Pair
}

// NewRound constructs an empty round.
func NewRound(number int) Round {
	return Round{
		Number:           number,
		Pairs:            make(map[Pair]bool),
		Paired:           make(map[string]bool),
		DrawOrderedPairs: []Pair{},
	}
}

// AddPair records one pair. The legacy odd-member holdout is deliberately not
// attached to ExtraID.
func (r *Round) AddPair(id1, id2 string) {
	if r.Pairs == nil {
		r.Pairs = make(map[Pair]bool)
	}
	if r.Paired == nil {
		r.Paired = make(map[string]bool)
	}
	pair := NewPair(id1, id2)
	r.Paired[id1] = true
	r.Paired[id2] = true
	r.Pairs[pair] = true
	r.DrawOrderedPairs = append(r.DrawOrderedPairs, pair)
}

// IsPaired reports whether id has already been used this round.
func (r Round) IsPaired(id string) bool {
	return r.Paired[id]
}

func (r Round) String() string {
	header := fmt.Sprintf("---------\nRound %d\n---------\n", r.Number)
	pairs := make([]string, 0, len(r.DrawOrderedPairs))
	for _, pair := range r.DrawOrderedPairs {
		pairs = append(pairs, pair.String())
	}
	return header + strings.Join(pairs, "\n") + "\n"
}

// MinimalMember is the pairing-specific member view.
type MinimalMember struct {
	ID            string
	Name          string
	Trait         string
	LastRoundWith map[string]int
}

// MembersMap indexes the active pairing population by member ID.
type MembersMap map[string]MinimalMember

// UpdateLastRoundWith symmetrically records a pairing. Missing members are
// ignored so malformed boundary data cannot manufacture phantom members.
func (members MembersMap) UpdateLastRoundWith(id1, id2 string, round int) {
	member1, ok1 := members[id1]
	member2, ok2 := members[id2]
	if !ok1 || !ok2 {
		return
	}
	if member1.LastRoundWith == nil {
		member1.LastRoundWith = make(map[string]int)
	}
	if member2.LastRoundWith == nil {
		member2.LastRoundWith = make(map[string]int)
	}
	member1.LastRoundWith[id2] = round
	member2.LastRoundWith[id1] = round
	members[id1] = member1
	members[id2] = member2
}

func (members MembersMap) String() string {
	var out strings.Builder
	out.WriteString("X's last round with Y\n")
	ids := members.IDs()
	for _, id := range ids {
		fmt.Fprintf(&out, "%s\n", id)
		partners := make([]string, 0, len(members[id].LastRoundWith))
		for partner := range members[id].LastRoundWith {
			partners = append(partners, partner)
		}
		sort.Strings(partners)
		for _, partner := range partners {
			fmt.Fprintf(&out, "\t%-30s : %d\n", partner, members[id].LastRoundWith[partner])
		}
		out.WriteString("\n")
	}
	return out.String()
}

// IDs returns member IDs in stable order.
func (members MembersMap) IDs() []string {
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (members MembersMap) clone() MembersMap {
	cloned := make(MembersMap, len(members))
	for id, member := range members {
		member.LastRoundWith = maps.Clone(member.LastRoundWith)
		cloned[id] = member
	}
	return cloned
}
