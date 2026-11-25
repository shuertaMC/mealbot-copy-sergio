package httpapi

import (
	"errors"
	"net/http"
)

// QueryParam returns exactly one value for key. Empty values are valid, while
// missing and duplicate keys share the legacy error text.
func QueryParam(r *http.Request, key string) (string, error) {
	queries, ok := r.URL.Query()[key]
	if !ok || len(queries) > 1 {
		return "", errors.New("Request query parameters must contain " + key)
	}
	return queries[0], nil
}

// QueryParams extracts one value for each key in order. Empty values are valid
// and a missing or duplicate key uses the distinct historical plural message.
func QueryParams(r *http.Request, keys []string) ([]string, error) {
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		queries, ok := r.URL.Query()[key]
		if !ok || len(queries) > 1 {
			return []string{}, errors.New("Request query parameters does not contain " + key)
		}
		values = append(values, queries[0])
	}
	return values, nil
}
