package mealbot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/johnamadeo/server"
)

const (
	// NoMaxRoundErr : TODO
	NoMaxRoundErr = "converting driver.Value type <nil>"

	// TimestampFormat : Postgres timestamp string template patterns can be found in https://www.postgresql.org/docs/8.1/functions-formatting.html
	TimestampFormat = "YYYY-MM-DD HH24:MI:ssZ"
)

// GetRoundsResponse : Data structure for storing the dates for each round
type GetRoundsResponse struct {
	Rounds []string `json:"rounds"`
}

var addRoundForHandler = addRound
var getRoundsForHandler = getRoundsFromDB
var removeRoundForHandler = removeRound
var rescheduleRoundForHandler = rescheduleRound

// AddRoundHandler : HTTP handler for scheduling a new round on a certain date
func AddRoundHandler(w http.ResponseWriter, r *http.Request) {
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

	err = addRoundForHandler(orgname, roundDate)
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	LogAndWrite(
		w,
		server.StrToBytes("Successfully scheduled a new round"),
		http.StatusCreated,
		function,
	)
}

// GetRoundsHandler : HTTP handler for retrieving the dates for all rounds scheduled
func GetRoundsHandler(w http.ResponseWriter, r *http.Request) {
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

	rounds, err := getRoundsForHandler(orgname)
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

// RoundHandler : Combined HTTP handler for rounds
func RoundHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		_, err := getQueryParam(r, "roundId")
		if err != nil {
			AddRoundHandler(w, r)
		} else {
			RescheduleRoundHandler(w, r)
		}
	} else if r.Method == "DELETE" {
		RemoveRoundHandler(w, r)
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

func getRoundsFromDB(orgname string) ([]string, error) {
	repository, db, err := openStoreAdapter()
	if err != nil {
		return []string{}, err
	}
	defer db.Close()
	return repository.ListRounds(context.Background(), orgname)
}

// RemoveRoundHandler : HTTP handler for cancelling a previously scheduled round
func RemoveRoundHandler(w http.ResponseWriter, r *http.Request) {
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

	err = removeRoundForHandler(orgname, roundID)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	LogAndWrite(
		w,
		server.StrToBytes("Successfully removed round"),
		http.StatusCreated,
		function,
	)
}

// RescheduleRoundHandler : HTTP handler for rescheduling the date of a particular round
func RescheduleRoundHandler(w http.ResponseWriter, r *http.Request) {
	function := "RescheduleRoundHandler"
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write(server.StrToBytes("Only POST requests are allowed at this route"))
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

	err = rescheduleRoundForHandler(orgname, roundDate, roundID)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	LogAndWrite(
		w,
		server.StrToBytes("Succesfully changed date of the round"),
		http.StatusCreated,
		function,
	)
}

func addRound(orgname string, roundDate string) error {
	repository, db, err := openStoreAdapter()
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = repository.AddRound(context.Background(), orgname, roundDate)
	return err
}

func removeRound(orgname string, roundID int) error {
	repository, db, err := openStoreAdapter()
	if err != nil {
		return err
	}
	defer db.Close()
	return repository.DeleteRound(context.Background(), orgname, roundID)
}

func rescheduleRound(orgname string, roundDate string, roundID int) error {
	repository, db, err := openStoreAdapter()
	if err != nil {
		return err
	}
	defer db.Close()
	return repository.RescheduleRound(context.Background(), orgname, roundDate, roundID)
}
