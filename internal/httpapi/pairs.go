package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/johnamadeo/mealbot/internal/domain"
)

// PairHistoryRepository is the persistence surface used to resolve pair
// history. ListMembers must include inactive members when activeOnly is false.
type PairHistoryRepository interface {
	ListPairs(context.Context, string) ([]domain.Round, error)
	ListMembers(context.Context, string, bool) ([]domain.Member, error)
}

// PairHistoryUseCases is the narrow handler-facing history contract.
type PairHistoryUseCases interface {
	ListHistory(context.Context, string) (GetPairsResponse, error)
}

// PairHistoryService resolves persisted member IDs into historical members.
type PairHistoryService struct{ repository PairHistoryRepository }

// NewPairHistoryService constructs history use cases over repository.
func NewPairHistoryService(repository PairHistoryRepository) *PairHistoryService {
	return &PairHistoryService{repository: repository}
}

// PairMemberResponse preserves the legacy pair-history member JSON shape.
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

// ListHistory includes inactive members and fails on dangling member IDs.
func (s *PairHistoryService) ListHistory(ctx context.Context, organization string) (GetPairsResponse, error) {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return GetPairsResponse{}, domain.NewValidationError("pair.organization", "must not be empty")
	}
	rounds, err := s.repository.ListPairs(ctx, organization)
	if err != nil {
		return GetPairsResponse{}, err
	}
	members, err := s.repository.ListMembers(ctx, organization, false)
	if err != nil {
		return GetPairsResponse{}, err
	}
	byEmail := make(map[string]domain.Member, len(members))
	for _, member := range members {
		byEmail[member.Email] = member
	}
	result := GetPairsResponse{RoundPairs: make([][]GetPairsResponsePair, 0, len(rounds))}
	for _, round := range rounds {
		group := make([]GetPairsResponsePair, 0, len(round.Pairs))
		for _, pair := range round.Pairs {
			member1, err := historicalMember(byEmail, organization, round.ID, pair.ID1)
			if err != nil {
				return GetPairsResponse{}, err
			}
			member2, err := historicalMember(byEmail, organization, round.ID, pair.ID2)
			if err != nil {
				return GetPairsResponse{}, err
			}
			resolved := GetPairsResponsePair{Member1: pairMember(member1), Member2: pairMember(member2)}
			if pair.ExtraID != "" {
				extra, err := historicalMember(byEmail, organization, round.ID, pair.ExtraID)
				if err != nil {
					return GetPairsResponse{}, err
				}
				resolved.ExtraMember = pairMember(extra)
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

func historicalMember(members map[string]domain.Member, organization string, roundID int, email string) (domain.Member, error) {
	member, ok := members[email]
	if !ok {
		return domain.Member{}, domain.NewIntegrityError("pair history", fmt.Sprintf("organization %q round %d references missing member %q", organization, roundID, email), nil)
	}
	return member, nil
}

func pairMember(member domain.Member) PairMemberResponse {
	return PairMemberResponse{Organization: member.Organization, Email: member.Email, Name: member.Name, Metadata: member.Metadata, LastRoundWith: member.LastRoundWith}
}
