package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// RoundsResponse is the legacy scheduled-round response.
type RoundsResponse struct {
	Rounds []string `json:"rounds"`
}

// RoundHandlers owns /rounds and /round using only its injected store and
// responder.
type RoundHandlers struct {
	store     RoundStore
	responder Responder
}

// NewRoundHandlers constructs the round transport boundary.
func NewRoundHandlers(store RoundStore, responder Responder) RoundHandlers {
	return RoundHandlers{store: store, responder: responder}
}

// ServeHTTP dispatches /round. A POST is a reschedule only when roundId has
// exactly one value; missing or duplicate roundId values retain the legacy add
// behavior.
func (h RoundHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		if _, err := QueryParam(r, "roundId"); err != nil {
			h.Add(w, r)
			return
		}
		h.Reschedule(w, r)
	case http.MethodDelete:
		h.Remove(w, r)
	default:
		h.responder.WriteError(w, errOnlyPOSTAndDELETERequests, http.StatusMethodNotAllowed, "RoundHandler")
	}
}

// Get handles GET /rounds.
func (h RoundHandlers) Get(w http.ResponseWriter, r *http.Request) {
	const function = "GetRoundsHandler"
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
		h.responder.WriteInternalServerError(w, errors.New("round store is unavailable"), function)
		return
	}
	rounds, err := h.store.ListRounds(r.Context(), organization)
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	encoded, err := json.Marshal(RoundsResponse{Rounds: rounds})
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	h.responder.Write(w, encoded, http.StatusOK, function)
}

// Add handles the add branch of POST /round.
func (h RoundHandlers) Add(w http.ResponseWriter, r *http.Request) {
	const function = "AddRoundHandler"
	if r.Method != http.MethodPost {
		h.responder.WriteError(w, errOnlyPOSTRequests, http.StatusMethodNotAllowed, function)
		return
	}
	values, err := QueryParams(r, []string{"org", "round"})
	if err != nil {
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	if h.store == nil {
		h.responder.WriteBadRequest(w, errors.New("round store is unavailable"), function)
		return
	}
	if _, err := h.store.AddRound(r.Context(), values[0], values[1]); err != nil {
		// Add is the one round mutation whose persistence failures have always
		// been exposed as bad requests.
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	h.responder.Write(w, MessageBytes("Successfully scheduled a new round"), http.StatusCreated, function)
}

// Remove handles DELETE /round.
func (h RoundHandlers) Remove(w http.ResponseWriter, r *http.Request) {
	const function = "RemoveRoundHandler"
	if r.Method != http.MethodDelete {
		h.responder.WriteError(w, errOnlyDELETERequests, http.StatusMethodNotAllowed, function)
		return
	}
	values, err := QueryParams(r, []string{"org", "roundId"})
	if err != nil {
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	roundID, err := strconv.Atoi(values[1])
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	if h.store == nil {
		h.responder.WriteInternalServerError(w, errors.New("round store is unavailable"), function)
		return
	}
	if err := h.store.DeleteRound(r.Context(), values[0], roundID); err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	h.responder.Write(w, MessageBytes("Successfully removed round"), http.StatusCreated, function)
}

// Reschedule handles the reschedule branch of POST /round.
func (h RoundHandlers) Reschedule(w http.ResponseWriter, r *http.Request) {
	const function = "RescheduleRoundHandler"
	if r.Method != http.MethodPost {
		// The legacy implementation uniquely wrote this method error as a raw
		// body instead of a Message envelope.
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(errOnlyPOSTRequests.Error()))
		return
	}
	values, err := QueryParams(r, []string{"org", "round", "roundId"})
	if err != nil {
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	roundID, err := strconv.Atoi(values[2])
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	if h.store == nil {
		h.responder.WriteInternalServerError(w, errors.New("round store is unavailable"), function)
		return
	}
	if err := h.store.RescheduleRound(r.Context(), values[0], values[1], roundID); err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	h.responder.Write(w, MessageBytes("Succesfully changed date of the round"), http.StatusCreated, function)
}
