package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnamadeo/mealbot/internal/domain"
)

type fakePairHistoryRepository struct {
	history      []domain.HistoricalRound
	err          error
	organization string
	calls        int
}

func (f *fakePairHistoryRepository) ListHistoricalPairs(_ context.Context, organization string) ([]domain.HistoricalRound, error) {
	f.organization = organization
	f.calls++
	return f.history, f.err
}

func TestPairHistoryMapsResolvedInactivePairsAndTriples(t *testing.T) {
	member1 := domain.Member{Organization: "o", Email: "a@x", Name: "A", Active: false, Metadata: map[string]string{"team": "1"}, LastRoundWith: map[string]int{"b@x": 1}}
	member2 := domain.Member{Organization: "o", Email: "b@x", Name: "B", Active: true}
	extra := domain.Member{Organization: "o", Email: "c@x", Name: "C", Active: false}
	repository := &fakePairHistoryRepository{
		history: []domain.HistoricalRound{
			{Round: domain.Round{ID: 0, ScheduledAt: time.Now()}, Pairs: []domain.HistoricalPair{}},
			{Round: domain.Round{ID: 1, ScheduledAt: time.Now()}, Pairs: []domain.HistoricalPair{{Pair: domain.Pair{ID1: "a@x", ID2: "b@x"}, Member1: member1, Member2: member2}}},
			{Round: domain.Round{ID: 2, ScheduledAt: time.Now()}, Pairs: []domain.HistoricalPair{{Pair: domain.Pair{ID1: "a@x", ID2: "b@x", ExtraID: "c@x"}, Member1: member1, Member2: member2, ExtraMember: &extra}}},
		},
	}
	result, err := NewPairHistoryService(repository).ListHistory(context.Background(), " o ")
	if err != nil || len(result.RoundPairs) != 3 || result.RoundPairs[0] == nil || len(result.RoundPairs[0]) != 0 || result.RoundPairs[1][0].Member1.Name != "A" || result.RoundPairs[1][0].ExtraMember.Email != "" || result.RoundPairs[2][0].ExtraMember.Name != "C" {
		t.Fatalf("ListHistory() = %#v, %v", result, err)
	}
	if repository.calls != 1 || repository.organization != "o" {
		t.Fatalf("ListHistoricalPairs calls = %d, organization = %q", repository.calls, repository.organization)
	}
	if got := result.RoundPairs[1][0].Member1; got.Organization != "" || got.Metadata != nil || got.LastRoundWith != nil || got.Email != "a@x" {
		t.Fatalf("pair member = %#v; want only email and name populated", got)
	}

	injected := domain.NewIntegrityError("pair history", "injected dangling reference", nil)
	repository.err = injected
	_, err = NewPairHistoryService(repository).ListHistory(context.Background(), "o")
	if err != injected {
		t.Fatalf("repository error = %v; want unchanged %v", err, injected)
	}
}

type fakePairHistoryUseCases struct {
	result       GetPairsResponse
	err          error
	organization string
	calls        int
}

func (f *fakePairHistoryUseCases) ListHistory(_ context.Context, organization string) (GetPairsResponse, error) {
	f.organization = organization
	f.calls++
	return f.result, f.err
}

func TestPairsHandlerContracts(t *testing.T) {
	tests := []struct {
		name, method, target string
		err                  error
		status               int
		body                 string
	}{
		{name: "missing org", method: http.MethodGet, target: "/pairs", status: http.StatusBadRequest, body: "{\"error\":\"invalid request\"}\n"},
		{name: "repeated org", method: http.MethodGet, target: "/pairs?org=a&org=b", status: http.StatusBadRequest, body: "{\"error\":\"invalid request\"}\n"},
		{name: "empty org", method: http.MethodGet, target: "/pairs?org=", status: http.StatusBadRequest, body: "{\"error\":\"invalid request\"}\n"},
		{name: "internal", method: http.MethodGet, target: "/pairs?org=o", err: errors.New("database password"), status: http.StatusInternalServerError, body: "{\"error\":\"internal server error\"}\n"},
		{name: "unsupported method", method: http.MethodPost, target: "/pairs?org=o", status: http.StatusMethodNotAllowed, body: "{\"error\":\"method not allowed\"}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakePairHistoryUseCases{err: test.err}
			recorder := httptest.NewRecorder()
			NewPairsHandler(service, nil).ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
			assertHTTPResponse(t, recorder, test.status, test.body, "application/json; charset=utf-8")
			if strings.Contains(recorder.Body.String(), "password") {
				t.Fatal("internal error leaked")
			}
			if test.status == http.StatusBadRequest || test.status == http.StatusMethodNotAllowed {
				if service.calls != 0 {
					t.Fatalf("invalid request invoked service %d times", service.calls)
				}
			}
		})
	}
}

func TestPairsHandlerSuccessStatusContentTypeAndExactGroupShape(t *testing.T) {
	member := func(email, name, team string, history map[string]int) PairMemberResponse {
		return PairMemberResponse{
			Organization:  "o",
			Email:         email,
			Name:          name,
			Metadata:      map[string]string{"team": team},
			LastRoundWith: history,
		}
	}
	result := GetPairsResponse{RoundPairs: [][]GetPairsResponsePair{
		{},
		{{
			Member1: member("a@x", "A", "red", map[string]int{"b@x": 2}),
			Member2: member("b@x", "B", "blue", map[string]int{"a@x": 2}),
		}},
		{{
			Member1:     member("c@x", "C", "green", map[string]int{"d@x": 3, "e@x": 3}),
			Member2:     member("d@x", "D", "yellow", map[string]int{"c@x": 3, "e@x": 3}),
			ExtraMember: member("e@x", "E", "purple", map[string]int{"c@x": 3, "d@x": 3}),
		}},
	}}
	service := &fakePairHistoryUseCases{result: result}
	recorder := httptest.NewRecorder()
	NewPairsHandler(service, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pairs?org=o", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("status=%d type=%q body=%q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
	if service.calls != 1 || service.organization != "o" {
		t.Fatalf("service calls=%d organization=%q", service.calls, service.organization)
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"roundPairs": []any{
		[]any{},
		[]any{map[string]any{
			"member1":     pairMemberJSON("o", "a@x", "A", "red", map[string]any{"b@x": float64(2)}),
			"member2":     pairMemberJSON("o", "b@x", "B", "blue", map[string]any{"a@x": float64(2)}),
			"extraMember": pairMemberJSON("", "", "", "", nil),
		}},
		[]any{map[string]any{
			"member1":     pairMemberJSON("o", "c@x", "C", "green", map[string]any{"d@x": float64(3), "e@x": float64(3)}),
			"member2":     pairMemberJSON("o", "d@x", "D", "yellow", map[string]any{"c@x": float64(3), "e@x": float64(3)}),
			"extraMember": pairMemberJSON("o", "e@x", "E", "purple", map[string]any{"c@x": float64(3), "d@x": float64(3)}),
		}},
	}}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("decoded pair history = %#v; want %#v", decoded, want)
	}
}

func pairMemberJSON(organization, email, name, team string, history map[string]any) map[string]any {
	var metadata any
	if team != "" {
		metadata = map[string]any{"team": team}
	}
	var lastRoundWith any
	if history != nil {
		lastRoundWith = history
	}
	return map[string]any{
		"Organization":  organization,
		"email":         email,
		"name":          name,
		"Metadata":      metadata,
		"LastRoundWith": lastRoundWith,
	}
}
