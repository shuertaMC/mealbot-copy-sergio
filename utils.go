package mealbot

import (
	"errors"
	"net/http"
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
		queries, ok := r.URL.Query()[key]
		if !ok || len(queries) > 1 {
			return []string{}, errors.New("Request query parameters does not contain " + key)
		}
		values = append(values, queries[0])
	}
	return values, nil
}
