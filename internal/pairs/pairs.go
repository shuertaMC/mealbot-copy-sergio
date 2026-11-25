// Package pairs implements the handler and query for stored pairings.
package pairs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/db"
	"github.com/johnamadeo/mealbot/internal/httputil"
	"github.com/johnamadeo/mealbot/internal/members"
)

// GetPairsResponsePair : Data structure for a pairing
type GetPairsResponsePair struct {
	Member1     members.Member `json:"member1"`
	Member2     members.Member `json:"member2"`
	ExtraMember members.Member `json:"extraMember"`
}

// GetPairsResponse : Data structure for storing pairings, separated by rounds
type GetPairsResponse struct {
	RoundPairs [][]GetPairsResponsePair `json:"roundPairs"`
}

// Handler serves the pairs route using DB for persistence.
type Handler struct {
	DB db.Querier
}

// NewHandler returns a Handler backed by q.
func NewHandler(q db.Querier) *Handler {
	return &Handler{DB: q}
}

// GetPairsHandler : HTTP Handler for getting pairings for an organization
func (h *Handler) GetPairsHandler(w http.ResponseWriter, r *http.Request) {
	function := "GetPairsHandler"
	if r.Method != "GET" {
		httputil.LogAndWriteErr(
			w,
			errors.New("Only GET requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	orgname, err := httputil.GetQueryParam(r, "org")
	if err != nil {
		httputil.LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	roundPairs, err := GetPairsFromDB(h.DB, orgname)
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	resp := GetPairsResponse{
		RoundPairs: roundPairs,
	}

	bytes, err := json.Marshal(resp)
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	httputil.LogAndWrite(w, bytes, http.StatusOK, function)
}

// GetPairsFromDB : Get all the pairings for a particular organization
func GetPairsFromDB(q db.Querier, orgname string) ([][]GetPairsResponsePair, error) {
	roundPairs := [][]GetPairsResponsePair{}

	// NOTE: as in the original implementation, an error here is not
	// propagated; members then resolve to empty Member values.
	activeMembers, _ := members.GetActiveMembersInPairFormat(q, orgname)
	membersMap := map[string]members.Member{}
	for _, member := range activeMembers {
		membersMap[member.Email] = member
	}

	round := 0

	for {
		pairs, err := getRoundPairs(q, orgname, round, membersMap)
		if err != nil {
			return roundPairs, err
		}

		if len(pairs) == 0 {
			break
		}

		roundPairs = append(roundPairs, pairs)
		round++
	}

	return roundPairs, nil
}

func getRoundPairs(
	q db.Querier,
	orgname string,
	round int,
	membersMap map[string]members.Member,
) ([]GetPairsResponsePair, error) {
	pairRows, err := q.Query(
		"SELECT id1, id2, extraId FROM pairs WHERE organization = $1 AND round = $2",
		orgname,
		round,
	)
	if err != nil {
		return nil, err
	}
	defer pairRows.Close()

	pairs := []GetPairsResponsePair{}
	for pairRows.Next() {
		var id1, id2 string
		var extraID sql.NullString
		err := pairRows.Scan(&id1, &id2, &extraID)
		if err != nil {
			return nil, err
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
	}

	return pairs, nil
}
