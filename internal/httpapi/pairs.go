package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/store"
)

// GetPairsResponsePair : Data structure for a pairing
type GetPairsResponsePair struct {
	Member1     store.Member `json:"member1"`
	Member2     store.Member `json:"member2"`
	ExtraMember store.Member `json:"extraMember"`
}

// GetPairsResponse : Data structure for storing pairings, separated by rounds
type GetPairsResponse struct {
	RoundPairs [][]GetPairsResponsePair `json:"roundPairs"`
}

// getPairs : GET /pairs?org=... gets the pairings of every round of an organization
func (s *Server) getPairs(w http.ResponseWriter, r *http.Request) {
	function := "GetPairsHandler"

	orgname, err := getQueryParam(r, "org")
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	pairs, err := s.store.GetPairs(r.Context(), orgname)
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	members, err := s.store.GetMembers(r.Context(), orgname, true)
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}
	// only name and email are exposed; inactive members come out as empty members
	membersMap := map[string]store.Member{}
	for _, member := range members {
		membersMap[member.Email] = store.Member{Name: member.Name, Email: member.Email}
	}

	roundPairs := make([][]GetPairsResponsePair, 0, len(pairs))
	for _, round := range pairs {
		responsePairs := make([]GetPairsResponsePair, 0, len(round))
		for _, pair := range round {
			responsePairs = append(responsePairs, GetPairsResponsePair{
				Member1:     membersMap[pair.ID1],
				Member2:     membersMap[pair.ID2],
				ExtraMember: membersMap[pair.ExtraID],
			})
		}
		roundPairs = append(roundPairs, responsePairs)
	}

	bytes, err := json.Marshal(GetPairsResponse{RoundPairs: roundPairs})
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	s.writeOK(w, bytes, function)
}
