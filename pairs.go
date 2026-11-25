package mealbot

import (
	"context"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/httpapi"
	internalstore "github.com/johnamadeo/mealbot/internal/store"
)

// GetPairsResponsePair : Data structure for a pairing
type GetPairsResponsePair struct {
	Member1     Member `json:"member1"`
	Member2     Member `json:"member2"`
	ExtraMember Member `json:"extraMember"`
}

// GetPairsResponse : Data structure for storing pairings, separated by rounds
type GetPairsResponse struct {
	RoundPairs [][]GetPairsResponsePair `json:"roundPairs"`
}

var getPairsForHandler = getPairsFromDB

type legacyPairHTTPStore struct{}

func (legacyPairHTTPStore) PairHistory(_ context.Context, organization string) ([][]internalstore.HistoricalPair, error) {
	roundPairs, err := getPairsForHandler(organization)
	if err != nil || roundPairs == nil {
		return nil, err
	}
	history := make([][]internalstore.HistoricalPair, 0, len(roundPairs))
	for _, round := range roundPairs {
		pairs := make([]internalstore.HistoricalPair, 0, len(round))
		for _, pair := range round {
			stored := internalstore.HistoricalPair{
				Member1: legacyStoredMember(pair.Member1),
				Member2: legacyStoredMember(pair.Member2),
			}
			if !isZeroLegacyMember(pair.ExtraMember) {
				extra := legacyStoredMember(pair.ExtraMember)
				stored.ExtraMember = &extra
			}
			pairs = append(pairs, stored)
		}
		history = append(history, pairs)
	}
	return history, nil
}

func legacyPairHandlers() httpapi.PairHandlers {
	return httpapi.NewPairHandlers(legacyPairHTTPStore{}, legacyResponder)
}

// GetPairsHandler : HTTP Handler for getting pairings for an organization
func GetPairsHandler(w http.ResponseWriter, r *http.Request) {
	legacyPairHandlers().Get(w, r)
}

// getPairsFromDB : Get all the pairings for a particular organization
func getPairsFromDB(orgname string) ([][]GetPairsResponsePair, error) {
	repository, db, err := openStoreAdapter()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	stored, err := repository.PairHistory(context.Background(), orgname)
	if err != nil {
		return nil, err
	}
	roundPairs := make([][]GetPairsResponsePair, 0, len(stored))
	for _, round := range stored {
		pairs := make([]GetPairsResponsePair, 0, len(round))
		for _, pair := range round {
			responsePair := GetPairsResponsePair{
				Member1: legacyMember(pair.Member1),
				Member2: legacyMember(pair.Member2),
			}
			if pair.ExtraMember != nil {
				responsePair.ExtraMember = legacyMember(*pair.ExtraMember)
			}
			pairs = append(pairs, responsePair)
		}
		roundPairs = append(roundPairs, pairs)
	}
	return roundPairs, nil
}

func legacyMember(member internalstore.Member) Member {
	return Member{
		Organization:  member.Organization,
		Email:         member.Email,
		Name:          member.Name,
		Metadata:      member.Metadata,
		LastRoundWith: member.LastRoundWith,
	}
}

func legacyStoredMember(member Member) internalstore.Member {
	return internalstore.Member{
		Organization:  member.Organization,
		Email:         member.Email,
		Name:          member.Name,
		Metadata:      member.Metadata,
		LastRoundWith: member.LastRoundWith,
	}
}

func isZeroLegacyMember(member Member) bool {
	return member.Organization == "" && member.Email == "" && member.Name == "" && member.Metadata == nil && member.LastRoundWith == nil
}
