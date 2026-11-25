package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseMemberCSVNormalizesRequiredHeadersAndMetadata(t *testing.T) {
	t.Parallel()
	members, err := ParseMemberCSV(strings.NewReader("\ufeff EMAIL , NaMe , Cohort ,Favorite Food\n alice@example.com , Alice ,Blue,pizza\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []MemberInput{{
		Name:  "Alice",
		Email: "alice@example.com",
		Metadata: map[string]string{
			"cohort":        "Blue",
			"favorite food": "pizza",
		},
	}}
	if !reflect.DeepEqual(members, want) {
		t.Fatalf("members = %#v, want %#v", members, want)
	}
}

func TestParseMemberCSVRejectsMalformedDocumentsWithoutLeakingMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		csv     string
		wantErr error
	}{
		{name: "empty document", csv: "", wantErr: ErrMemberNameHeaderRequired},
		{name: "missing name", csv: "email,team\na@example.com,red\n", wantErr: ErrMemberNameHeaderRequired},
		{name: "missing email", csv: "name,team\nAlice,red\n", wantErr: ErrMemberEmailHeaderRequired},
		{name: "headers only", csv: "name,email\n", wantErr: ErrMemberRecordsRequired},
		{name: "duplicate name", csv: "name, NAME ,email\nAlice,A,a@example.com\n"},
		{name: "duplicate metadata", csv: "name,email,Team, team \nAlice,a@example.com,x,y\n"},
		{name: "empty header", csv: "name,email, \nAlice,a@example.com,x\n"},
		{name: "short row", csv: "name,email,team\nAlice,a@example.com\n"},
		{name: "long row", csv: "name,email\nAlice,a@example.com,very-secret-metadata\n"},
		{name: "empty name", csv: "name,email\n  ,a@example.com\n"},
		{name: "empty email", csv: "name,email\nAlice,  \n"},
		{name: "duplicate identity", csv: "name,email\nAlice,A@example.com\nAlias,a@example.com\n"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseMemberCSV(strings.NewReader(test.csv))
			if err == nil {
				t.Fatal("ParseMemberCSV() succeeded, want error")
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if strings.Contains(err.Error(), "very-secret-metadata") {
				t.Fatalf("error exposes metadata: %v", err)
			}
		})
	}
}

func TestPlanMemberSynchronizationCopiesAndNormalizesInput(t *testing.T) {
	t.Parallel()
	metadata := map[string]string{" Cohort ": "blue"}
	input := []MemberInput{{Name: " Alice ", Email: " a@example.com ", Metadata: metadata}}
	plan, err := PlanMemberSynchronization("org", input)
	if err != nil {
		t.Fatal(err)
	}
	metadata[" Cohort "] = "changed"
	input[0].Name = "changed"
	if plan.Members[0].Name != "Alice" || plan.Members[0].Email != "a@example.com" || plan.Members[0].Metadata["cohort"] != "blue" {
		t.Fatalf("plan was not normalized and copied: %#v", plan)
	}
}

func TestPlanMemberSynchronizationRejectsUnsafeInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		org     string
		members []MemberInput
	}{
		{name: "empty organization", members: []MemberInput{{Name: "A", Email: "a@example.com"}}},
		{name: "no records", org: "org"},
		{name: "empty name", org: "org", members: []MemberInput{{Email: "a@example.com"}}},
		{name: "empty email", org: "org", members: []MemberInput{{Name: "A"}}},
		{name: "case insensitive duplicate", org: "org", members: []MemberInput{{Name: "A", Email: "A@example.com"}, {Name: "B", Email: "a@example.com"}}},
		{name: "reserved metadata", org: "org", members: []MemberInput{{Name: "A", Email: "a@example.com", Metadata: map[string]string{" Name ": "masked"}}}},
		{name: "ambiguous metadata", org: "org", members: []MemberInput{{Name: "A", Email: "a@example.com", Metadata: map[string]string{"Team": "one", " team ": "two"}}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := PlanMemberSynchronization(test.org, test.members); err == nil {
				t.Fatal("PlanMemberSynchronization() succeeded, want error")
			}
		})
	}
}
