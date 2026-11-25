package httputil

import (
	"errors"
	"net/http"
)

// GetQueryParam returns the single value of the query parameter key.
func GetQueryParam(r *http.Request, key string) (string, error) {
	queries, ok := r.URL.Query()[key]
	if !ok || len(queries) > 1 {
		return "", errors.New("Request query parameters must contain " + key)
	}
	return queries[0], nil
}

// GetQueryParams returns the single value of each of the query parameters keys,
// in order.
func GetQueryParams(r *http.Request, keys []string) ([]string, error) {
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
