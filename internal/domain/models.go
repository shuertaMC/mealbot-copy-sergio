package domain

import (
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"
)

// Organization identifies an administered Mealbot organization.
type Organization struct {
	Name  string `json:"name"`
	Admin string `json:"admin"`
	// CrossMatchTrait is nil when no trait preference is configured. A
	// non-nil value names the member metadata key used for cross-matching.
	CrossMatchTrait *string `json:"crossMatchTrait,omitempty"`
}

// NewOrganization validates and constructs an Organization. The trait value
// is copied so later caller mutation cannot change the organization.
func NewOrganization(name, admin string, crossMatchTrait *string) (Organization, error) {
	organization := Organization{
		Name:  strings.TrimSpace(name),
		Admin: strings.TrimSpace(admin),
	}
	if crossMatchTrait != nil {
		trait := strings.TrimSpace(*crossMatchTrait)
		organization.CrossMatchTrait = &trait
	}
	if err := organization.Validate(); err != nil {
		return Organization{}, err
	}
	return organization, nil
}

// Validate verifies the organization's required identity and optional trait.
func (o Organization) Validate() error {
	if strings.TrimSpace(o.Name) == "" {
		return NewValidationError("organization.name", "must not be empty")
	}
	if strings.TrimSpace(o.Admin) == "" {
		return NewValidationError("organization.admin", "must not be empty")
	}
	if o.CrossMatchTrait != nil && strings.TrimSpace(*o.CrossMatchTrait) == "" {
		return NewValidationError("organization.crossMatchTrait", "must not be empty when set")
	}
	return nil
}

// Member is an organization member, including arbitrary import metadata,
// lifecycle state, and the latest round shared with every known partner.
type Member struct {
	Organization  string            `json:"organization"`
	Email         string            `json:"email"`
	Name          string            `json:"name"`
	Metadata      map[string]string `json:"metadata"`
	Active        bool              `json:"active"`
	LastRoundWith map[string]int    `json:"lastRoundWith"`
}

// NewMember validates and constructs an active Member with defensive copies
// of metadata and pairing history.
func NewMember(organization, email, name string, metadata map[string]string, lastRoundWith map[string]int) (Member, error) {
	member := Member{
		Organization:  strings.TrimSpace(organization),
		Email:         strings.TrimSpace(email),
		Name:          strings.TrimSpace(name),
		Metadata:      cloneStringMap(metadata),
		Active:        true,
		LastRoundWith: cloneHistory(lastRoundWith),
	}
	if err := member.Validate(); err != nil {
		return Member{}, err
	}
	return member, nil
}

// Validate verifies member identity, metadata keys, and round history.
func (m Member) Validate() error {
	if strings.TrimSpace(m.Organization) == "" {
		return NewValidationError("member.organization", "must not be empty")
	}
	if strings.TrimSpace(m.Name) == "" {
		return NewValidationError("member.name", "must not be empty")
	}
	if !validEmail(m.Email) {
		return NewValidationError("member.email", "must be a valid email address")
	}
	for key := range m.Metadata {
		if strings.TrimSpace(key) == "" {
			return NewValidationError("member.metadata", "keys must not be empty")
		}
	}
	for partner, round := range m.LastRoundWith {
		if strings.TrimSpace(partner) == "" {
			return NewValidationError("member.lastRoundWith", "partner IDs must not be empty")
		}
		if partner == m.Email {
			return NewValidationError("member.lastRoundWith", "must not contain the member itself")
		}
		if round < -1 {
			return NewValidationError("member.lastRoundWith", "round values must be -1 or non-negative")
		}
	}
	return nil
}

// Clone returns a deep copy of the member's map-backed state.
func (m Member) Clone() Member {
	m.Metadata = cloneStringMap(m.Metadata)
	m.LastRoundWith = cloneHistory(m.LastRoundWith)
	return m
}

// Pair is a canonical two- or three-member pairing. ExtraID is empty for a
// pair and contains the third, lexicographically greatest ID for a triple.
type Pair struct {
	ID1     string `json:"id1"`
	ID2     string `json:"id2"`
	ExtraID string `json:"extraId,omitempty"`
}

// NewPair validates two member IDs plus at most one optional extra member ID,
// and returns them in canonical lexicographic order.
func NewPair(id1, id2 string, extraID ...string) (Pair, error) {
	if len(extraID) > 1 {
		return Pair{}, NewValidationError("pair.extraId", "at most one extra member is allowed")
	}
	ids := []string{strings.TrimSpace(id1), strings.TrimSpace(id2)}
	if len(extraID) == 1 {
		ids = append(ids, strings.TrimSpace(extraID[0]))
	}
	for _, id := range ids {
		if id == "" {
			return Pair{}, NewValidationError("pair.memberId", "must not be empty")
		}
	}
	sort.Strings(ids)
	for index := 1; index < len(ids); index++ {
		if ids[index] == ids[index-1] {
			return Pair{}, NewValidationError("pair.memberId", "member IDs must be distinct")
		}
	}
	pair := Pair{ID1: ids[0], ID2: ids[1]}
	if len(ids) == 3 {
		pair.ExtraID = ids[2]
	}
	return pair, nil
}

// Validate verifies that the pair is non-empty, distinct, and canonical.
func (p Pair) Validate() error {
	optional := []string(nil)
	if p.ExtraID != "" {
		optional = []string{p.ExtraID}
	}
	canonical, err := NewPair(p.ID1, p.ID2, optional...)
	if err != nil {
		return err
	}
	if canonical != p {
		return NewValidationError("pair", "member IDs are not in canonical order")
	}
	return nil
}

// Members returns the pair's two or three member IDs in canonical order.
func (p Pair) Members() []string {
	members := []string{p.ID1, p.ID2}
	if p.ExtraID != "" {
		members = append(members, p.ExtraID)
	}
	return members
}

// Round is one scheduled organization pairing round and its completion state.
type Round struct {
	Organization string    `json:"organization"`
	ID           int       `json:"id"`
	ScheduledAt  time.Time `json:"scheduledAt"`
	Done         bool      `json:"done"`
	Pairs        []Pair    `json:"pairs,omitempty"`
	// ClaimToken identifies the scheduler lease that owns this round and is
	// never exposed through HTTP JSON.
	ClaimToken string `json:"-"`
}

// NewRound validates and constructs an unfinished scheduled round.
func NewRound(organization string, id int, scheduledAt time.Time) (Round, error) {
	round := Round{
		Organization: strings.TrimSpace(organization),
		ID:           id,
		ScheduledAt:  scheduledAt,
		Pairs:        []Pair{},
	}
	if err := round.Validate(); err != nil {
		return Round{}, err
	}
	return round, nil
}

// Validate verifies round identity, schedule, pair validity, and single-round
// membership uniqueness.
func (r Round) Validate() error {
	if strings.TrimSpace(r.Organization) == "" {
		return NewValidationError("round.organization", "must not be empty")
	}
	if r.ID < 0 {
		return NewValidationError("round.id", "must be non-negative")
	}
	if r.ScheduledAt.IsZero() {
		return NewValidationError("round.scheduledAt", "must be set")
	}
	seen := make(map[string]struct{})
	for index, pair := range r.Pairs {
		if err := pair.Validate(); err != nil {
			return NewValidationError(fmt.Sprintf("round.pairs[%d]", index), err.Error())
		}
		for _, member := range pair.Members() {
			if _, exists := seen[member]; exists {
				return NewValidationError("round.pairs", fmt.Sprintf("member %q appears more than once", member))
			}
			seen[member] = struct{}{}
		}
	}
	return nil
}

func validEmail(raw string) bool {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return false
	}
	parsed, err := mail.ParseAddress(raw)
	return err == nil && parsed.Address == raw && strings.Contains(raw, "@")
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return map[string]string{}
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneHistory(source map[string]int) map[string]int {
	if source == nil {
		return map[string]int{}
	}
	clone := make(map[string]int, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
