// Package rounds implements the round handlers and queries (scheduling,
// listing, removing and rescheduling rounds) plus the runner that executes a
// pairing round against the database and sends the resulting emails.
package rounds

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/johnamadeo/mealbot/internal/db"
	"github.com/johnamadeo/mealbot/internal/httputil"
)

const (
	// NoMaxRoundErr : message produced by older database/sql versions when scanning a NULL MAX(id)
	// into an int. Retained for reference; AddRound now scans into a sql.NullInt64.
	NoMaxRoundErr = "converting driver.Value type <nil>"

	// TimestampFormat : Postgres timestamp string template patterns can be found in https://www.postgresql.org/docs/8.1/functions-formatting.html
	TimestampFormat = "YYYY-MM-DD HH24:MI:ssZ"
)

// GetRoundsResponse : Data structure for storing the dates for each round
type GetRoundsResponse struct {
	Rounds []string `json:"rounds"`
}

// Handler serves the round routes using DB for persistence.
type Handler struct {
	DB db.Querier
}

// NewHandler returns a Handler backed by q.
func NewHandler(q db.Querier) *Handler {
	return &Handler{DB: q}
}

// AddRoundHandler : HTTP handler for scheduling a new round on a certain date
func (h *Handler) AddRoundHandler(w http.ResponseWriter, r *http.Request) {
	function := "AddRoundHandler"
	if r.Method != "POST" {
		httputil.LogAndWriteErr(
			w,
			errors.New("Only POST requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	// TODO: Verify 'round' is a datestring in the YYYY-MM-DDTHH:mm:ss[Z] format
	values, err := httputil.GetQueryParams(r, []string{"org", "round"})
	if err != nil {
		httputil.LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	orgname := values[0]
	roundDate := values[1]

	err = AddRound(h.DB, orgname, roundDate)
	if err != nil {
		httputil.LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	httputil.LogAndWrite(
		w,
		httputil.StrToBytes("Successfully scheduled a new round"),
		http.StatusCreated,
		function,
	)
}

// GetRoundsHandler : HTTP handler for retrieving the dates for all rounds scheduled
func (h *Handler) GetRoundsHandler(w http.ResponseWriter, r *http.Request) {
	function := "GetRoundsHandler"
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

	rounds, err := GetRoundsFromDB(h.DB, orgname)
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	resp := GetRoundsResponse{Rounds: rounds}
	bytes, err := json.Marshal(resp)
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	httputil.LogAndWrite(w, bytes, http.StatusOK, function)
}

// RoundHandler : Combined HTTP handler for rounds
func (h *Handler) RoundHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		_, err := httputil.GetQueryParam(r, "roundId")
		if err != nil {
			h.AddRoundHandler(w, r)
		} else {
			h.RescheduleRoundHandler(w, r)
		}
	} else if r.Method == "DELETE" {
		h.RemoveRoundHandler(w, r)
	} else {
		httputil.LogAndWriteErr(
			w,
			errors.New("Only POST and DELETE requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			"RoundHandler",
		)
		return
	}
}

// RemoveRoundHandler : HTTP handler for cancelling a previously scheduled round
func (h *Handler) RemoveRoundHandler(w http.ResponseWriter, r *http.Request) {
	function := "RemoveRoundHandler"
	if r.Method != "DELETE" {
		httputil.LogAndWriteErr(
			w,
			errors.New("Only DELETE requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	values, err := httputil.GetQueryParams(r, []string{"org", "roundId"})
	if err != nil {
		httputil.LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	orgname := values[0]
	roundID, err := strconv.Atoi(values[1])
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	err = RemoveRound(h.DB, orgname, roundID)
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	httputil.LogAndWrite(
		w,
		httputil.StrToBytes("Successfully removed round"),
		http.StatusCreated,
		function,
	)
}

// RescheduleRoundHandler : HTTP handler for rescheduling the date of a particular round
func (h *Handler) RescheduleRoundHandler(w http.ResponseWriter, r *http.Request) {
	function := "RescheduleRoundHandler"
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write(httputil.StrToBytes("Only POST requests are allowed at this route"))
		return
	}

	values, err := httputil.GetQueryParams(r, []string{"org", "round", "roundId"})
	if err != nil {
		httputil.LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	orgname := values[0]
	roundDate := values[1]
	roundID, err := strconv.Atoi(values[2])
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	err = RescheduleRound(h.DB, orgname, roundDate, roundID)
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	httputil.LogAndWrite(
		w,
		httputil.StrToBytes("Succesfully changed date of the round"),
		http.StatusCreated,
		function,
	)
}

// GetRoundsFromDB returns the scheduled dates of the organization's rounds, ordered by round id.
func GetRoundsFromDB(q db.Querier, orgname string) ([]string, error) {
	rows, err := q.Query(
		"SELECT to_char(scheduled_date, $1) FROM rounds WHERE organization = $2 ORDER BY id ASC",
		TimestampFormat,
		orgname,
	)
	if err != nil {
		return []string{}, err
	}
	defer rows.Close()

	rounds := []string{}
	for rows.Next() {
		var roundDate string
		err = rows.Scan(&roundDate)
		if err != nil {
			return []string{}, err
		}

		rounds = append(rounds, roundDate)
	}

	return rounds, nil
}

// AddRound schedules a new round (with the next free id) for orgname on roundDate.
func AddRound(q db.Querier, orgname string, roundDate string) error {
	rows, err := q.Query(
		"SELECT MAX(id) FROM rounds WHERE organization = $1",
		orgname,
	)

	if err != nil {
		return err
	}
	defer rows.Close()

	// MAX(id) is NULL when the organization has no rounds yet; the first round gets id 0.
	maxRoundID := int64(-1)
	for rows.Next() {
		var maxID sql.NullInt64
		err := rows.Scan(&maxID)
		if err != nil {
			return err
		}
		if maxID.Valid {
			maxRoundID = maxID.Int64
		}
		break
	}

	// Release the connection before executing the insert.
	rows.Close()

	_, err = q.Exec(
		"INSERT INTO rounds (organization, id, scheduled_date, done) VALUES ($1, $2, $3, $4)",
		orgname,
		maxRoundID+1,
		roundDate,
		false,
	)
	if err != nil {
		return err
	}

	return nil
}

// RemoveRound deletes a round and shifts the ids of all later rounds down by one.
func RemoveRound(q db.Querier, orgname string, roundID int) error {
	_, err := q.Exec(
		"DELETE FROM rounds WHERE organization = $1 AND id = $2",
		orgname,
		roundID,
	)
	if err != nil {
		return err
	}

	for {
		result, err := q.Exec(
			"UPDATE rounds SET id = $1 WHERE organization = $2 AND id = $3",
			roundID,
			orgname,
			roundID+1,
		)
		if err != nil {
			return err
		}
		if numRows, _ := result.RowsAffected(); numRows == 0 {
			break
		}

		roundID++
	}

	return nil
}

// RescheduleRound changes the scheduled date of an existing round.
func RescheduleRound(q db.Querier, orgname string, roundDate string, roundID int) error {
	_, err := q.Exec(
		"UPDATE rounds SET scheduled_date = $1 WHERE organization = $2 AND id = $3",
		roundDate,
		orgname,
		roundID,
	)
	if err != nil {
		return err
	}

	return nil
}
