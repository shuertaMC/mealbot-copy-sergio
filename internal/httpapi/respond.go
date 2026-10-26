package httpapi

import (
	"errors"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/server"
)

// writeErr logs err and answers with status and a {"Message": "..."} body.
func (s *Server) writeErr(w http.ResponseWriter, err error, status int, function string) {
	if status >= http.StatusInternalServerError {
		s.Logger.Error("request failed", "function", function, "status", status, "error", err)
	} else {
		s.Logger.Warn("request rejected", "function", function, "status", status, "error", err)
	}
	w.WriteHeader(status)
	w.Write(server.ErrToBytes(err))
}

func (s *Server) writeBadRequest(w http.ResponseWriter, err error, function string) {
	s.writeErr(w, err, http.StatusBadRequest, function)
}

func (s *Server) writeInternalError(w http.ResponseWriter, err error, function string) {
	s.writeErr(w, err, http.StatusInternalServerError, function)
}

// writeOK answers 200 with body.
//
// Several handlers have always logged "201 Created" but actually answered 200,
// and existing clients rely on what is on the wire, so those keep answering 200.
func (s *Server) writeOK(w http.ResponseWriter, body []byte, function string) {
	s.Logger.Debug("request handled", "function", function, "status", http.StatusOK)
	w.Write(body)
}

// getQueryParam returns the single value of the query parameter key.
func getQueryParam(r *http.Request, key string) (string, error) {
	queries, ok := r.URL.Query()[key]
	if !ok || len(queries) > 1 {
		return "", errors.New("Request query parameters must contain " + key)
	}
	return queries[0], nil
}

// getQueryParams returns the single value of each of the query parameters keys.
func getQueryParams(r *http.Request, keys []string) ([]string, error) {
	values := []string{}
	for _, key := range keys {
		queries, ok := r.URL.Query()[key]
		if !ok || len(queries) > 1 {
			return []string{}, errors.New("Request query parameters does not contain " + key)
		}
		values = append(values, queries[0])
	}
	return values, nil
}
