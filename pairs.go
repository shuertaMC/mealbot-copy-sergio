package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/server"
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

	roundPairs, err := getPairsFromDB(orgname)
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
	roundPairs := [][]GetPairsResponsePair{}

	db, err := server.CreateDBConnection(LocalDBConnection)
	defer db.Close()
	if err != nil {
		return roundPairs, err
	}

	members, err := getActiveMembersFromDBInPairFormat(orgname)
	membersMap := map[string]Member{}
	for _, member := range members {
		membersMap[member.Email] = member
	}

	round := 0

	for {
		pairRows, err := db.Query(
			"SELECT id1, id2, extraId FROM pairs WHERE organization = $1 AND round = $2",
			orgname,
			round,
		)
		if err != nil {
			return roundPairs, err
		}

		numPairs := 0
		pairs := []GetPairsResponsePair{}
		for pairRows.Next() {
			var id1, id2 string
			var extraID sql.NullString
			err := pairRows.Scan(&id1, &id2, &extraID)
			if err != nil {
				return roundPairs, err
			}

			if extraID.Valid {
				pairs = append(pairs, GetPairsResponsePair{
					Member1:     membersMap[id1],
					Member2:     membersMap[id2],
					ExtraMember: membersMap[extraID.String],
				})
			} else {
				pairs = append(pairs, GetPairsResponsePair{
					Member1: membersMap[id1],
					Member2: membersMap[id2],
				})
			}

			numPairs++
		}

		if numPairs == 0 {
			break
		}

		roundPairs = append(roundPairs, pairs)
		round++
	}

	return roundPairs, nil
}
