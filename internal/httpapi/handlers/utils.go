package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/db"
)

func getQueryParam(r *http.Request, key string) (string, error) {
	queries, ok := r.URL.Query()[key]
	if !ok || len(queries) > 1 {
		return "", errors.New("Request query parameters must contain " + key)
	}
	return queries[0], nil
}

func getQueryParams(r *http.Request, keys []string) ([]string, error) {
	values := []string{}
	for _, key := range keys {
		value, err := getQueryParam(r, key)
		if err != nil {
			return []string{}, err
		}
		values = append(values, value)
	}
	return values, nil
}

// LogAndWriteErr logs err and responds with the status and a JSON error body.
func LogAndWriteErr(w http.ResponseWriter, err error, status int, function string) {
	slog.Error(err.Error(), "status", status, "function", function)
	w.WriteHeader(status)
	w.Write(db.ErrToBytes(err))
}

// LogAndWrite logs and writes the body. As in the original implementation the
// status is only logged: the response status is the implicit 200.
func LogAndWrite(w http.ResponseWriter, bytes []byte, status int, function string) {
	slog.Debug("response", "status", status, "function", function)
	w.Write(bytes)
}

// LogAndWriteStatusBadRequest responds with 400.
func LogAndWriteStatusBadRequest(w http.ResponseWriter, err error, function string) {
	LogAndWriteErr(w, err, http.StatusBadRequest, function)
}

// LogAndWriteStatusInternalServerError responds with 500.
func LogAndWriteStatusInternalServerError(w http.ResponseWriter, err error, function string) {
	LogAndWriteErr(w, err, http.StatusInternalServerError, function)
}
