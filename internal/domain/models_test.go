package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOrganizationConstructionAndValidation(t *testing.T) {
	trait := " team "
	organization, err := NewOrganization(" Lunch Club ", " admin@example.test ", &trait)
	if err != nil {
		t.Fatalf("NewOrganization() error = %v", err)
	}
	trait = "changed"
	if organization.Name != "Lunch Club" || organization.Admin != "admin@example.test" {
		t.Errorf("organization = %#v", organization)
	}
	if organization.CrossMatchTrait == nil || *organization.CrossMatchTrait != "team" {
		t.Errorf("CrossMatchTrait = %v", organization.CrossMatchTrait)
	}

	tests := []struct {
		name         string
		organization Organization
		field        string
	}{
		{name: "missing name", organization: Organization{Admin: "admin"}, field: "organization.name"},
		{name: "missing admin", organization: Organization{Name: "org"}, field: "organization.admin"},
		{name: "blank configured trait", organization: Organization{Name: "org", Admin: "admin", CrossMatchTrait: stringPointer(" ")}, field: "organization.crossMatchTrait"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.organization.Validate()
			if !IsValidation(err) {
				t.Fatalf("Validate() error = %v, want validation", err)
			}
			if !strings.Contains(err.Error(), test.field) {
				t.Errorf("Validate() error = %q, want field %q", err, test.field)
			}
		})
	}

	withoutTrait, err := NewOrganization("org", "admin", nil)
	if err != nil || withoutTrait.CrossMatchTrait != nil {
		t.Errorf("NewOrganization(nil trait) = %#v, %v", withoutTrait, err)
	}
}

func TestMemberConstructionCloneAndValidation(t *testing.T) {
	metadata := map[string]string{"team": "blue"}
	history := map[string]int{"partner@example.test": -1}
	member, err := NewMember(" org ", "member@example.test", " Member ", metadata, history)
	if err != nil {
		t.Fatalf("NewMember() error = %v", err)
	}
	metadata["team"] = "changed"
	history["partner@example.test"] = 9
	if !member.Active || member.Organization != "org" || member.Name != "Member" {
		t.Errorf("member = %#v", member)
	}
	if member.Metadata["team"] != "blue" || member.LastRoundWith["partner@example.test"] != -1 {
		t.Errorf("constructor did not copy maps: %#v", member)
	}
	clone := member.Clone()
	clone.Metadata["team"] = "green"
	clone.LastRoundWith["partner@example.test"] = 2
	if member.Metadata["team"] != "blue" || member.LastRoundWith["partner@example.test"] != -1 {
		t.Errorf("Clone() shared map state with original")
	}

	tests := []struct {
		name   string
		member Member
		field  string
	}{
		{name: "organization", member: Member{Name: "name", Email: "a@example.test"}, field: "member.organization"},
		{name: "name", member: Member{Organization: "org", Email: "a@example.test"}, field: "member.name"},
		{name: "email", member: Member{Organization: "org", Name: "name", Email: "not-email"}, field: "member.email"},
		{name: "metadata key", member: validMember(map[string]string{" ": "value"}, nil), field: "member.metadata"},
		{name: "empty history ID", member: validMember(nil, map[string]int{"": 0}), field: "member.lastRoundWith"},
		{name: "self history", member: validMember(nil, map[string]int{"member@example.test": 0}), field: "member.lastRoundWith"},
		{name: "invalid history round", member: validMember(nil, map[string]int{"other@example.test": -2}), field: "member.lastRoundWith"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.member.Validate()
			if !IsValidation(err) || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("Validate() error = %v, want validation for %s", err, test.field)
			}
		})
	}

	withoutMaps, err := NewMember("org", "new@example.test", "New", nil, nil)
	if err != nil {
		t.Fatalf("NewMember(nil maps) error = %v", err)
	}
	withoutMaps.Metadata["safe"] = "yes"
	withoutMaps.LastRoundWith["other@example.test"] = -1
}

func TestNewPairCanonicalizesPairsAndTriples(t *testing.T) {
	tests := []struct {
		name  string
		id1   string
		id2   string
		extra []string
		want  Pair
	}{
		{name: "ordered pair", id1: "a", id2: "b", want: Pair{ID1: "a", ID2: "b"}},
		{name: "reversed pair", id1: "b", id2: "a", want: Pair{ID1: "a", ID2: "b"}},
		{name: "trims pair", id1: " b ", id2: " a ", want: Pair{ID1: "a", ID2: "b"}},
		{name: "canonical triple", id1: "c", id2: "a", extra: []string{"b"}, want: Pair{ID1: "a", ID2: "b", ExtraID: "c"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pair, err := NewPair(test.id1, test.id2, test.extra...)
			if err != nil {
				t.Fatalf("NewPair() error = %v", err)
			}
			if pair != test.want {
				t.Errorf("NewPair() = %#v, want %#v", pair, test.want)
			}
			if err := pair.Validate(); err != nil {
				t.Errorf("Validate() error = %v", err)
			}
			members := pair.Members()
			members[0] = "changed"
			if pair.ID1 != test.want.ID1 {
				t.Error("Members() exposed mutable pair state")
			}
		})
	}
}

func TestPairRejectsInvalidMembership(t *testing.T) {
	tests := []struct {
		name  string
		id1   string
		id2   string
		extra []string
	}{
		{name: "empty first", id2: "b"},
		{name: "empty second", id1: "a"},
		{name: "duplicate", id1: "a", id2: "a"},
		{name: "duplicate triple", id1: "a", id2: "b", extra: []string{"a"}},
		{name: "too many extras", id1: "a", id2: "b", extra: []string{"c", "d"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewPair(test.id1, test.id2, test.extra...)
			if !IsValidation(err) {
				t.Fatalf("NewPair() error = %v, want validation", err)
			}
		})
	}

	if err := (Pair{ID1: "b", ID2: "a"}).Validate(); !IsValidation(err) {
		t.Fatalf("non-canonical Pair.Validate() error = %v, want validation", err)
	}
}

func TestRoundConstructionAndValidation(t *testing.T) {
	scheduled := time.Date(2027, time.January, 5, 12, 30, 0, 0, time.UTC)
	round, err := NewRound(" org ", 2, scheduled)
	if err != nil {
		t.Fatalf("NewRound() error = %v", err)
	}
	if round.Organization != "org" || round.ID != 2 || !round.ScheduledAt.Equal(scheduled) || round.Done || round.Pairs == nil {
		t.Errorf("round = %#v", round)
	}
	round.ClaimToken = "scheduler-secret"
	encoded, err := json.Marshal(round)
	if err != nil {
		t.Fatalf("json.Marshal(round): %v", err)
	}
	if strings.Contains(string(encoded), "scheduler-secret") || strings.Contains(string(encoded), "ClaimToken") {
		t.Fatalf("round JSON exposed claim token: %s", encoded)
	}

	pairAB, _ := NewPair("a", "b")
	pairCD, _ := NewPair("c", "d")
	valid := round
	valid.Pairs = []Pair{pairAB, pairCD}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid round error = %v", err)
	}

	tests := []struct {
		name  string
		round Round
		field string
	}{
		{name: "organization", round: Round{ID: 0, ScheduledAt: scheduled}, field: "round.organization"},
		{name: "negative ID", round: Round{Organization: "org", ID: -1, ScheduledAt: scheduled}, field: "round.id"},
		{name: "zero schedule", round: Round{Organization: "org", ID: 0}, field: "round.scheduledAt"},
		{name: "invalid pair", round: Round{Organization: "org", ID: 0, ScheduledAt: scheduled, Pairs: []Pair{{ID1: "b", ID2: "a"}}}, field: "round.pairs[0]"},
		{name: "duplicate member", round: Round{Organization: "org", ID: 0, ScheduledAt: scheduled, Pairs: []Pair{pairAB, {ID1: "b", ID2: "c"}}}, field: "round.pairs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.round.Validate()
			if !IsValidation(err) || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("Validate() error = %v, want validation for %s", err, test.field)
			}
		})
	}
}

func stringPointer(value string) *string { return &value }

func validMember(metadata map[string]string, history map[string]int) Member {
	return Member{
		Organization:  "org",
		Email:         "member@example.test",
		Name:          "Member",
		Metadata:      metadata,
		Active:        true,
		LastRoundWith: history,
	}
}
