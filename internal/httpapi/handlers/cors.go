package handlers

import (
	"net/http"
)

const (
	// AccessControlAllowHeaders : https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Access-Control-Allow-Headers
	AccessControlAllowHeaders = "Authorization, Content-Type, Origin, Accept, token"
)

// Cors : Create handler that sets up CORS policy https://developer.mozilla.org/en-US/docs/Web/HTTP/CORS
func Cors(handler http.Handler) http.Handler {
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
