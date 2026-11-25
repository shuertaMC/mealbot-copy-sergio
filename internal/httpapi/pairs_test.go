package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnamadeo/mealbot/internal/domain"
)

type fakePairHistoryRepository struct {
	rounds  []domain.Round
	members []domain.Member
	err     error
}

func (f *fakePairHistoryRepository) ListPairs(context.Context, string) ([]domain.Round, error) {
	return f.rounds, f.err
}
func (f *fakePairHistoryRepository) ListMembers(context.Context, string, bool) ([]domain.Member, error) {
	return f.members, f.err
}

func TestPairHistoryResolvesInactivePairsAndTriples(t *testing.T) {
	repository := &fakePairHistoryRepository{
		rounds: []domain.Round{
			{ID: 0, ScheduledAt: time.Now(), Pairs: []domain.Pair{{ID1: "a@x", ID2: "b@x"}}},
			{ID: 1, ScheduledAt: time.Now(), Pairs: []domain.Pair{{ID1: "a@x", ID2: "b@x", ExtraID: "c@x"}}},
		},
		members: []domain.Member{
			{Organization: "o", Email: "a@x", Name: "A", Active: false, Metadata: map[string]string{"team": "1"}, LastRoundWith: map[string]int{"b@x": 1}},
			{Organization: "o", Email: "b@x", Name: "B", Active: true},
			{Organization: "o", Email: "c@x", Name: "C", Active: false},
		},
	}
	result, err := NewPairHistoryService(repository).ListHistory(context.Background(), "o")
	if err != nil || len(result.RoundPairs) != 2 || result.RoundPairs[0][0].Member1.Name != "A" || result.RoundPairs[0][0].ExtraMember.Email != "" || result.RoundPairs[1][0].ExtraMember.Name != "C" {
		t.Fatalf("ListHistory() = %#v, %v", result, err)
	}

	repository.rounds[0].Pairs[0].ID2 = "missing@x"
	_, err = NewPairHistoryService(repository).ListHistory(context.Background(), "o")
	if !domain.IsIntegrity(err) || !strings.Contains(err.Error(), "missing@x") {
		t.Fatalf("dangling history error = %v", err)
	}
}

type fakePairHistoryUseCases struct{ err error }

func (f fakePairHistoryUseCases) ListHistory(context.Context, string) (GetPairsResponse, error) {
	return GetPairsResponse{RoundPairs: [][]GetPairsResponsePair{{{Member1: PairMemberResponse{Email: "a@x"}, Member2: PairMemberResponse{Email: "b@x"}}}}}, f.err
}

func TestPairsHandlerContracts(t *testing.T) {
	tests := []struct {
		method, target string
		err            error
		status         int
		contains       string
	}{
		{http.MethodGet, "/pairs?org=o", nil, http.StatusOK, `"roundPairs"`},
		{http.MethodGet, "/pairs", nil, http.StatusBadRequest, `"invalid request"`},
		{http.MethodGet, "/pairs?org=o", errors.New("database password"), http.StatusInternalServerError, `"internal server error"`},
		{http.MethodPost, "/pairs?org=o", nil, http.StatusMethodNotAllowed, `"method not allowed"`},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		NewPairsHandler(fakePairHistoryUseCases{err: test.err}, nil).ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
		if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), test.contains) || strings.Contains(recorder.Body.String(), "password") {
			t.Errorf("%s %s = %d %q", test.method, test.target, recorder.Code, recorder.Body.String())
		}
	}
}
