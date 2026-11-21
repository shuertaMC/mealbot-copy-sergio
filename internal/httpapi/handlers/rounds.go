package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/johnamadeo/mealbot/internal/db"
)

// GetRoundsResponse : Data structure for storing the dates for each round
type GetRoundsResponse struct {
	Rounds []string `json:"rounds"`
}

// AddRound : HTTP handler for scheduling a new round on a certain date
func (h *Handler) AddRound(w http.ResponseWriter, r *http.Request) {
	function := "AddRoundHandler"
	if r.Method != "POST" {
		LogAndWriteErr(
			w,
			errors.New("Only POST requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	// TODO: Verify 'round' is a datestring in the YYYY-MM-DDTHH:mm:ss[Z] format
	values, err := getQueryParams(r, []string{"org", "round"})
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	orgname := values[0]
	roundDate := values[1]

	err = h.Store.AddRound(r.Context(), orgname, roundDate)
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	LogAndWrite(
		w,
		db.StrToBytes("Successfully scheduled a new round"),
		http.StatusCreated,
		function,
	)
}

// GetRounds : HTTP handler for retrieving the dates for all rounds scheduled
func (h *Handler) GetRounds(w http.ResponseWriter, r *http.Request) {
	function := "GetRoundsHandler"
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

	rounds, err := h.Store.GetRounds(r.Context(), orgname)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	resp := GetRoundsResponse{Rounds: rounds}
	bytes, err := json.Marshal(resp)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	LogAndWrite(w, bytes, http.StatusOK, function)
}

// Round : Combined HTTP handler for rounds
func (h *Handler) Round(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		_, err := getQueryParam(r, "roundId")
		if err != nil {
			h.AddRound(w, r)
		} else {
			h.RescheduleRound(w, r)
		}
	} else if r.Method == "DELETE" {
		h.RemoveRound(w, r)
	} else {
		LogAndWriteErr(
			w,
			errors.New("Only POST and DELETE requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			"RoundHandler",
		)
		return
	}
}

// RemoveRound : HTTP handler for cancelling a previously scheduled round
func (h *Handler) RemoveRound(w http.ResponseWriter, r *http.Request) {
	function := "RemoveRoundHandler"
	if r.Method != "DELETE" {
		LogAndWriteErr(
			w,
			errors.New("Only DELETE requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	values, err := getQueryParams(r, []string{"org", "roundId"})
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	orgname := values[0]
	roundID, err := strconv.Atoi(values[1])
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	err = h.Store.RemoveRound(r.Context(), orgname, roundID)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	LogAndWrite(
		w,
		db.StrToBytes("Successfully removed round"),
		http.StatusCreated,
		function,
	)
}

// RescheduleRound : HTTP handler for rescheduling the date of a particular round
func (h *Handler) RescheduleRound(w http.ResponseWriter, r *http.Request) {
	function := "RescheduleRoundHandler"
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write(db.StrToBytes("Only POST requests are allowed at this route"))
		return
	}

	values, err := getQueryParams(r, []string{"org", "round", "roundId"})
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	orgname := values[0]
	roundDate := values[1]
	roundID, err := strconv.Atoi(values[2])
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	err = h.Store.RescheduleRound(r.Context(), orgname, roundDate, roundID)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	LogAndWrite(
		w,
		db.StrToBytes("Succesfully changed date of the round"),
		http.StatusCreated,
		function,
	)
}
