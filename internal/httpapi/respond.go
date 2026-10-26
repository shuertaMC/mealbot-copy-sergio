package httpapi

import (
	"encoding/json"
	"errors"
	"io"
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

// writeJSON marshals v and answers 200 with it, or 500 if marshalling fails.
func (s *Server) writeJSON(w http.ResponseWriter, v any, function string) {
	bytes, err := json.Marshal(v)
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}
	s.writeOK(w, bytes, function)
}

// decodeBody reads the request body and unmarshals it into v.
func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	bytes, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, v)
}

// singleQueryValue returns the value of the query parameter key, and false
// unless it is present exactly once.
func singleQueryValue(r *http.Request, key string) (string, bool) {
	queries, ok := r.URL.Query()[key]
	if !ok || len(queries) > 1 {
		return "", false
	}
	return queries[0], true
}

// getQueryParam returns the single value of the query parameter key.
func getQueryParam(r *http.Request, key string) (string, error) {
	value, ok := singleQueryValue(r, key)
	if !ok {
		return "", errors.New("Request query parameters must contain " + key)
	}
	return value, nil
}

// getQueryParams returns the single value of each of the query parameters keys.
func getQueryParams(r *http.Request, keys []string) ([]string, error) {
	values := []string{}
	for _, key := range keys {
		value, ok := singleQueryValue(r, key)
		if !ok {
			return []string{}, errors.New("Request query parameters does not contain " + key)
		}
		values = append(values, value)
	}
	return values, nil
}
