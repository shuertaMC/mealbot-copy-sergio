package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/store"
)

// PairMember preserves the historical member JSON shape. Metadata and pair
// history intentionally retain their exported Go field names, while email and
// name use the original lower-case JSON names. Active is persistence-only.
type PairMember struct {
	Organization  string
	Email         string `json:"email"`
	Name          string `json:"name"`
	Metadata      map[string]string
	LastRoundWith map[string]int
}

// PairResponse is one historical pairing. ExtraMember deliberately is not
// omitempty: SQL NULL, empty IDs, and unresolved IDs all serialize as the
// legacy zero-value member object.
type PairResponse struct {
	Member1     PairMember `json:"member1"`
	Member2     PairMember `json:"member2"`
	ExtraMember PairMember `json:"extraMember"`
}

// PairsResponse groups historical pairs by consecutive round.
type PairsResponse struct {
	RoundPairs [][]PairResponse `json:"roundPairs"`
}

// PairHandlers owns GET /pairs using only its injected store and responder.
type PairHandlers struct {
	store     PairStore
	responder Responder
}

// NewPairHandlers constructs the pair-history transport boundary.
func NewPairHandlers(store PairStore, responder Responder) PairHandlers {
	return PairHandlers{store: store, responder: responder}
}

// Get handles GET /pairs.
func (h PairHandlers) Get(w http.ResponseWriter, r *http.Request) {
	const function = "GetPairsHandler"
	if r.Method != http.MethodGet {
		h.responder.WriteError(w, errOnlyGETRequests, http.StatusMethodNotAllowed, function)
		return
	}
	organization, err := QueryParam(r, "org")
	if err != nil {
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	if h.store == nil {
		h.responder.WriteInternalServerError(w, errors.New("pair store is unavailable"), function)
		return
	}
	history, err := h.store.PairHistory(r.Context(), organization)
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	encoded, err := json.Marshal(PairsResponse{RoundPairs: pairResponses(history)})
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	// GetPairsHandler historically called WriteHeader before Write. Retaining
	// that order keeps Content-Type unset instead of allowing net/http to sniff
	// application JSON as text/plain.
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

func pairResponses(history [][]store.HistoricalPair) [][]PairResponse {
	if history == nil {
		return nil
	}
	result := make([][]PairResponse, 0, len(history))
	for _, round := range history {
		pairs := make([]PairResponse, 0, len(round))
		for _, pair := range round {
			response := PairResponse{
				Member1: pairMember(pair.Member1),
				Member2: pairMember(pair.Member2),
			}
			if pair.ExtraMember != nil {
				response.ExtraMember = pairMember(*pair.ExtraMember)
			}
			pairs = append(pairs, response)
		}
		result = append(result, pairs)
	}
	return result
}

func pairMember(member store.Member) PairMember {
	return PairMember{
		Organization:  member.Organization,
		Email:         member.Email,
		Name:          member.Name,
		Metadata:      member.Metadata,
		LastRoundWith: member.LastRoundWith,
	}
}
