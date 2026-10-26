package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/johnamadeo/mealbot/internal/server"
)

// GetRoundsResponse : Data structure for storing the dates for each round
type GetRoundsResponse struct {
	Rounds []string `json:"rounds"`
}

// getRounds : GET /rounds?org=... retrieves the dates for all rounds scheduled
func (s *Server) getRounds(w http.ResponseWriter, r *http.Request) {
	function := "GetRoundsHandler"

	orgname, err := getQueryParam(r, "org")
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	rounds, err := s.store.GetRounds(r.Context(), orgname)
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	bytes, err := json.Marshal(GetRoundsResponse{Rounds: rounds})
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	s.writeOK(w, bytes, function)
}

// postRound : POST /round schedules a new round, or reschedules one when 'roundId' is given
func (s *Server) postRound(w http.ResponseWriter, r *http.Request) {
	if _, err := getQueryParam(r, "roundId"); err != nil {
		s.addRound(w, r)
	} else {
		s.rescheduleRound(w, r)
	}
}

// addRound : POST /round?org=...&round=... schedules a new round on a certain date
func (s *Server) addRound(w http.ResponseWriter, r *http.Request) {
	function := "AddRoundHandler"

	// TODO: Verify 'round' is a datestring in the YYYY-MM-DDTHH:mm:ss[Z] format
	values, err := getQueryParams(r, []string{"org", "round"})
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	if err := s.store.AddRound(r.Context(), values[0], values[1]); err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	s.writeOK(w, server.StrToBytes("Successfully scheduled a new round"), function)
}

// rescheduleRound : POST /round?org=...&round=...&roundId=... changes the date of a particular round
func (s *Server) rescheduleRound(w http.ResponseWriter, r *http.Request) {
	function := "RescheduleRoundHandler"

	values, err := getQueryParams(r, []string{"org", "round", "roundId"})
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	roundID, err := strconv.Atoi(values[2])
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	if err := s.store.RescheduleRound(r.Context(), values[0], values[1], roundID); err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	s.writeOK(w, server.StrToBytes("Succesfully changed date of the round"), function)
}

// removeRound : DELETE /round?org=...&roundId=... cancels a previously scheduled round
func (s *Server) removeRound(w http.ResponseWriter, r *http.Request) {
	function := "RemoveRoundHandler"

	values, err := getQueryParams(r, []string{"org", "roundId"})
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	roundID, err := strconv.Atoi(values[1])
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	if err := s.store.RemoveRound(r.Context(), values[0], roundID); err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	s.writeOK(w, server.StrToBytes("Successfully removed round"), function)
}
