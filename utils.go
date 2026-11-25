package mealbot

import (
	"net/http"

	"github.com/johnamadeo/mealbot/internal/httpapi"
)

func getQueryParam(r *http.Request, key string) (string, error) {
	return httpapi.QueryParam(r, key)
}

func getQueryParams(r *http.Request, keys []string) ([]string, error) {
	return httpapi.QueryParams(r, keys)
}
