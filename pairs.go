package mealbot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

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

// GetPairsHandler : HTTP Handler for getting pairings for an organization
func GetPairsHandler(w http.ResponseWriter, r *http.Request) {
	function := "GetPairsHandler"
	if r.Method != "GET" {
		LogAndWriteErr(
			w,
			errors.New("Only GET requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	orgname, err := getQueryParam(r, "org")
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	roundPairs, err := getPairsForHandler(orgname)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	resp := GetPairsResponse{
		RoundPairs: roundPairs,
	}

	bytes, err := json.Marshal(resp)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(bytes)
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
