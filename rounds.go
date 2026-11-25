package mealbot

import (
	"context"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/httpapi"
)

const (
	// NoMaxRoundErr : TODO
	NoMaxRoundErr = "converting driver.Value type <nil>"

	// TimestampFormat : Postgres timestamp string template patterns can be found in https://www.postgresql.org/docs/8.1/functions-formatting.html
	TimestampFormat = "YYYY-MM-DD HH24:MI:ssZ"
)

// GetRoundsResponse remains as a compatibility alias while the transport DTO
// is owned by internal/httpapi.
type GetRoundsResponse = httpapi.RoundsResponse

var addRoundForHandler = addRound
var getRoundsForHandler = getRoundsFromDB
var removeRoundForHandler = removeRound
var rescheduleRoundForHandler = rescheduleRound

type legacyRoundHTTPStore struct{}

func (legacyRoundHTTPStore) ListRounds(_ context.Context, organization string) ([]string, error) {
	return getRoundsForHandler(organization)
}

func (legacyRoundHTTPStore) AddRound(_ context.Context, organization, scheduled string) (int, error) {
	err := addRoundForHandler(organization, scheduled)
	return 0, err
}

func (legacyRoundHTTPStore) RescheduleRound(_ context.Context, organization, scheduled string, roundID int) error {
	return rescheduleRoundForHandler(organization, scheduled, roundID)
}

func (legacyRoundHTTPStore) DeleteRound(_ context.Context, organization string, roundID int) error {
	return removeRoundForHandler(organization, roundID)
}

func legacyRoundHandlers() httpapi.RoundHandlers {
	return httpapi.NewRoundHandlers(legacyRoundHTTPStore{}, legacyResponder)
}

// AddRoundHandler : HTTP handler for scheduling a new round on a certain date
func AddRoundHandler(w http.ResponseWriter, r *http.Request) {
	legacyRoundHandlers().Add(w, r)
}

// GetRoundsHandler : HTTP handler for retrieving the dates for all rounds scheduled
func GetRoundsHandler(w http.ResponseWriter, r *http.Request) {
	legacyRoundHandlers().Get(w, r)
}

// RoundHandler : Combined HTTP handler for rounds
func RoundHandler(w http.ResponseWriter, r *http.Request) {
	legacyRoundHandlers().ServeHTTP(w, r)
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
	legacyRoundHandlers().Remove(w, r)
}

// RescheduleRoundHandler : HTTP handler for rescheduling the date of a particular round
func RescheduleRoundHandler(w http.ResponseWriter, r *http.Request) {
	legacyRoundHandlers().Reschedule(w, r)
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
