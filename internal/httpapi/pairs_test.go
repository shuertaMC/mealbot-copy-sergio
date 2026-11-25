package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func assertPairHTTP(t *testing.T, handler http.Handler, method, target string, status int, body, contentType string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d (body %q)", recorder.Code, status, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != body {
		t.Fatalf("body = %q, want %q", got, body)
	}
	if got := recorder.Header().Get("Content-Type"); got != contentType {
		t.Fatalf("Content-Type = %q, want %q", got, contentType)
	}
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
		Active:        false,
	}
	first := store.Member{
		Organization:  "alpha",
		Email:         "first@example.com",
		Name:          "First",
		Metadata:      map[string]string{"cohort": "2025", "team": "red"},
		LastRoundWith: map[string]int{"second@example.com": 0},
		Active:        false,
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
	assertPairHTTP(t, http.HandlerFunc(handlers.Get), http.MethodGet, "/pairs?org=alpha", http.StatusOK, want, "")
	if storeFake.organization != "alpha" {
		t.Fatalf("organization = %q, want alpha", storeFake.organization)
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
			assertPairHTTP(t, http.HandlerFunc(handlers.Get), http.MethodGet, "/pairs?org=alpha", http.StatusOK, test.body, "")
		})
	}
}

func TestPairHandlersFailures(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		store  PairStore
		status int
		body   string
	}{
		{"wrong method", http.MethodPost, "/pairs?org=alpha", &fakePairStore{}, http.StatusMethodNotAllowed, `{"Message":"Only GET requests are allowed at this route"}`},
		{"missing organization", http.MethodGet, "/pairs", &fakePairStore{}, http.StatusBadRequest, `{"Message":"Request query parameters must contain org"}`},
		{"duplicate organization", http.MethodGet, "/pairs?org=one&org=two", &fakePairStore{}, http.StatusBadRequest, `{"Message":"Request query parameters must contain org"}`},
		{"store failure", http.MethodGet, "/pairs?org=alpha", &fakePairStore{err: errors.New("pair failure")}, http.StatusInternalServerError, `{"Message":"pair failure"}`},
		{"nil store", http.MethodGet, "/pairs?org=alpha", nil, http.StatusInternalServerError, `{"Message":"pair store is unavailable"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handlers := NewPairHandlers(test.store, NewResponder(nil))
			assertPairHTTP(t, http.HandlerFunc(handlers.Get), test.method, test.target, test.status, test.body, "")
		})
	}
}

func TestPairHandlersAcceptEmptyOrganization(t *testing.T) {
	storeFake := &fakePairStore{history: [][]store.HistoricalPair{}}
	handlers := NewPairHandlers(storeFake, NewResponder(nil))
	assertPairHTTP(t, http.HandlerFunc(handlers.Get), http.MethodGet, "/pairs?org=", http.StatusOK, `{"roundPairs":[]}`, "")
	if storeFake.organization != "" {
		t.Fatalf("organization = %q, want empty", storeFake.organization)
	}
}
