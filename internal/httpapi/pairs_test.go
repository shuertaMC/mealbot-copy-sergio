package httpapi

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/johnamadeo/mealbot/internal/store"
)

type fakePairStore struct {
	history      [][]store.HistoricalPair
	err          error
	organization string
}

func (s *fakePairStore) PairHistory(_ context.Context, organization string) ([][]store.HistoricalPair, error) {
	s.organization = organization
	return s.history, s.err
}

func TestPairHandlersExactMemberAndExtraMemberRepresentation(t *testing.T) {
	emptyExtraID := ""
	unresolvedExtraID := "missing@example.com"
	extra := store.Member{
		Organization:  "alpha",
		Email:         "extra@example.com",
		Name:          "Extra",
		Metadata:      map[string]string{"cohort": "2027"},
		LastRoundWith: map[string]int{"first@example.com": 0},
		Active:        true,
	}
	first := store.Member{
		Organization:  "alpha",
		Email:         "first@example.com",
		Name:          "First",
		Metadata:      map[string]string{"cohort": "2025", "team": "red"},
		LastRoundWith: map[string]int{"second@example.com": 0},
		Active:        true,
	}
	second := store.Member{
		Organization:  "alpha",
		Email:         "second@example.com",
		Name:          "Second",
		Metadata:      map[string]string{},
		LastRoundWith: map[string]int{},
		Active:        true,
	}
	storeFake := &fakePairStore{history: [][]store.HistoricalPair{{
		{Member1: first, Member2: second},
		{Member1: first, Member2: second, ExtraID: &emptyExtraID},
		{Member1: first, Member2: second, ExtraID: &unresolvedExtraID},
		{Member1: first, Member2: second, ExtraMember: &extra},
	}, {}}}
	handlers := NewPairHandlers(storeFake, NewResponder(nil))
	want := `{"roundPairs":[[{"member1":{"Organization":"alpha","email":"first@example.com","name":"First","Metadata":{"cohort":"2025","team":"red"},"LastRoundWith":{"second@example.com":0}},"member2":{"Organization":"alpha","email":"second@example.com","name":"Second","Metadata":{},"LastRoundWith":{}},"extraMember":{"Organization":"","email":"","name":"","Metadata":null,"LastRoundWith":null}},{"member1":{"Organization":"alpha","email":"first@example.com","name":"First","Metadata":{"cohort":"2025","team":"red"},"LastRoundWith":{"second@example.com":0}},"member2":{"Organization":"alpha","email":"second@example.com","name":"Second","Metadata":{},"LastRoundWith":{}},"extraMember":{"Organization":"","email":"","name":"","Metadata":null,"LastRoundWith":null}},{"member1":{"Organization":"alpha","email":"first@example.com","name":"First","Metadata":{"cohort":"2025","team":"red"},"LastRoundWith":{"second@example.com":0}},"member2":{"Organization":"alpha","email":"second@example.com","name":"Second","Metadata":{},"LastRoundWith":{}},"extraMember":{"Organization":"","email":"","name":"","Metadata":null,"LastRoundWith":null}},{"member1":{"Organization":"alpha","email":"first@example.com","name":"First","Metadata":{"cohort":"2025","team":"red"},"LastRoundWith":{"second@example.com":0}},"member2":{"Organization":"alpha","email":"second@example.com","name":"Second","Metadata":{},"LastRoundWith":{}},"extraMember":{"Organization":"alpha","email":"extra@example.com","name":"Extra","Metadata":{"cohort":"2027"},"LastRoundWith":{"first@example.com":0}}}],[]]}`
	assertHTTPResponse(t, http.HandlerFunc(handlers.Get), http.MethodGet, "/pairs?org=alpha", http.StatusOK, want, "")
	if storeFake.organization != "alpha" {
		t.Fatalf("organization = %q, want alpha", storeFake.organization)
	}
}

func TestPairResponsesKeepZeroMembersForInactiveAndUnresolvedIDs(t *testing.T) {
	inactiveExtraID := "inactive@example.com"
	missingExtraID := "missing@example.com"
	history := [][]store.HistoricalPair{{
		{Member1: store.Member{}, Member2: store.Member{Email: "active@example.com", Active: true}, ExtraID: &inactiveExtraID},
		{Member1: store.Member{Email: "active@example.com", Active: true}, Member2: store.Member{}, ExtraID: &missingExtraID},
	}}

	got := pairResponses(history)
	want := [][]PairResponse{{
		{Member1: PairMember{}, Member2: PairMember{Email: "active@example.com"}, ExtraMember: PairMember{}},
		{Member1: PairMember{Email: "active@example.com"}, Member2: PairMember{}, ExtraMember: PairMember{}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pairResponses() = %#v, want %#v", got, want)
	}
}

func TestPairHandlersNilAndEmptyHistories(t *testing.T) {
	tests := []struct {
		name    string
		history [][]store.HistoricalPair
		body    string
	}{
		{"nil", nil, `{"roundPairs":null}`},
		{"empty", [][]store.HistoricalPair{}, `{"roundPairs":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handlers := NewPairHandlers(&fakePairStore{history: test.history}, NewResponder(nil))
			assertHTTPResponse(t, http.HandlerFunc(handlers.Get), http.MethodGet, "/pairs?org=alpha", http.StatusOK, test.body, "")
		})
	}
}

func TestPairsEndpointMethodRestriction(t *testing.T) {
	handlers := NewPairHandlers(&fakePairStore{}, NewResponder(nil))
	assertHTTPResponse(t, http.HandlerFunc(handlers.Get), http.MethodPost, "/pairs?org=alpha", http.StatusMethodNotAllowed, `{"Message":"Only GET requests are allowed at this route"}`, "")
}

func TestPairsEndpointRepositoryFailure(t *testing.T) {
	t.Run("store error", func(t *testing.T) {
		handlers := NewPairHandlers(&fakePairStore{err: errors.New("pair failure")}, NewResponder(nil))
		assertHTTPResponse(t, http.HandlerFunc(handlers.Get), http.MethodGet, "/pairs?org=alpha", http.StatusInternalServerError, `{"Message":"pair failure"}`, "")
	})
	t.Run("missing store", func(t *testing.T) {
		handlers := NewPairHandlers(nil, NewResponder(nil))
		assertHTTPResponse(t, http.HandlerFunc(handlers.Get), http.MethodGet, "/pairs?org=alpha", http.StatusInternalServerError, `{"Message":"pair store is unavailable"}`, "")
	})
}

func TestPairHandlersQueryFailures(t *testing.T) {
	assertGetOrganizationQueryFailures(t, "/pairs", func() http.Handler {
		return http.HandlerFunc(NewPairHandlers(&fakePairStore{}, NewResponder(nil)).Get)
	})
}

func TestPairHandlersAcceptEmptyOrganization(t *testing.T) {
	storeFake := &fakePairStore{history: [][]store.HistoricalPair{}}
	handlers := NewPairHandlers(storeFake, NewResponder(nil))
	assertHTTPResponse(t, http.HandlerFunc(handlers.Get), http.MethodGet, "/pairs?org=", http.StatusOK, `{"roundPairs":[]}`, "")
	if storeFake.organization != "" {
		t.Fatalf("organization = %q, want empty", storeFake.organization)
	}
}
