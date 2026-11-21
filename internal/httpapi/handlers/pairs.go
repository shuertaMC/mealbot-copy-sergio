package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/store"
)

// GetPairsResponse : Data structure for storing pairings, separated by rounds
type GetPairsResponse struct {
	RoundPairs [][]store.PairedMembers `json:"roundPairs"`
}

// GetPairs : HTTP Handler for getting pairings for an organization
func (h *Handler) GetPairs(w http.ResponseWriter, r *http.Request) {
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

	roundPairs, err := h.Store.GetPairs(r.Context(), orgname)
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
