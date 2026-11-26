package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/johnamadeo/mealbot/internal/domain"
)

// PairHistoryRepository is the persistence surface used to load pair history
// with all stored member references already resolved.
type PairHistoryRepository interface {
	ListHistoricalPairs(context.Context, string) ([]domain.HistoricalRound, error)
}

// PairHistoryUseCases is the narrow handler-facing history contract.
type PairHistoryUseCases interface {
	ListHistory(context.Context, string) (GetPairsResponse, error)
}

// PairHistoryService maps resolved historical members into transport values.
type PairHistoryService struct{ repository PairHistoryRepository }

// NewPairHistoryService constructs history use cases over repository.
func NewPairHistoryService(repository PairHistoryRepository) *PairHistoryService {
	return &PairHistoryService{repository: repository}
}

// PairMemberResponse preserves the legacy pair-history member JSON shape. Only
// email and name are populated; the remaining fields serialize as legacy
// zero values.
type PairMemberResponse struct {
	Organization  string
	Email         string `json:"email"`
	Name          string `json:"name"`
	Metadata      map[string]string
	LastRoundWith map[string]int
}

// GetPairsResponsePair is a resolved pair or triple. ExtraMember remains a
// zero-value object for pairs, matching the legacy response contract.
type GetPairsResponsePair struct {
	Member1     PairMemberResponse `json:"member1"`
	Member2     PairMemberResponse `json:"member2"`
	ExtraMember PairMemberResponse `json:"extraMember"`
}

// GetPairsResponse groups pairings in ascending round order.
type GetPairsResponse struct {
	RoundPairs [][]GetPairsResponsePair `json:"roundPairs"`
}

// ListHistory preserves resolved inactive members and repository integrity
// errors while mapping provider-neutral history to the legacy response shape.
func (s *PairHistoryService) ListHistory(ctx context.Context, organization string) (GetPairsResponse, error) {
	organization = strings.TrimSpace(organization)
	if err := domain.RequireNonEmpty("pair.organization", organization); err != nil {
		return GetPairsResponse{}, err
	}
	rounds, err := s.repository.ListHistoricalPairs(ctx, organization)
	if err != nil {
		return GetPairsResponse{}, err
	}
	result := GetPairsResponse{RoundPairs: make([][]GetPairsResponsePair, 0, len(rounds))}
	for _, round := range rounds {
		group := make([]GetPairsResponsePair, 0, len(round.Pairs))
		for _, pair := range round.Pairs {
			resolved := GetPairsResponsePair{Member1: pairMember(pair.Member1), Member2: pairMember(pair.Member2)}
			if pair.ExtraMember != nil {
				resolved.ExtraMember = pairMember(*pair.ExtraMember)
			}
			group = append(group, resolved)
		}
		result.RoundPairs = append(result.RoundPairs, group)
	}
	return result, nil
}

// NewPairsHandler constructs authenticated GET /pairs?org= behavior.
func NewPairsHandler(service PairHistoryUseCases, logger *slog.Logger) http.Handler {
	responses := newResponder(logger)
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			responses.methodNotAllowed(w, http.MethodGet)
			return
		}
		organization, err := QueryValue(request, "org")
		if err != nil {
			responses.failure(request.Context(), w, "list pair history", err)
			return
		}
		result, err := service.ListHistory(request.Context(), organization)
		if err != nil {
			responses.failure(request.Context(), w, "list pair history", err)
			return
		}
		responses.json(w, http.StatusOK, result)
	})
}

func pairMember(member domain.Member) PairMemberResponse {
	return PairMemberResponse{Email: member.Email, Name: member.Name}
}
