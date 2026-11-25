// Package httputil holds the HTTP helpers shared by all handler packages:
// middleware chaining, CORS, query-parameter helpers and logging/response writers.
package httputil

import (
	"net/http"
)

const (
	// AccessControlAllowHeaders : https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Access-Control-Allow-Headers
	AccessControlAllowHeaders = "Authorization, Content-Type, Origin, Accept, token"
	// AccessControlAllowOrigin = "http://localhost:3000"
	// AccessControlAllowOrigin  = "https://mealbot-web.herokuapp.com"
)

// GetCorsHandler : Create handler that sets up CORS policy https://developer.mozilla.org/en-US/docs/Web/HTTP/CORS
func GetCorsHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// TODO: Get rid of temporary AccessControlAllowOrigin debugging setup
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Header().Add("Access-Control-Allow-Headers", AccessControlAllowHeaders)
		w.Header().Add("Access-Control-Allow-Methods", "GET, POST, DELETE")

		if r.Method == "OPTIONS" {
			return
		}

		handler.ServeHTTP(w, r)
	})
}
