package mealbot

import (
	"net/http"

	"github.com/johnamadeo/mealbot/internal/httpapi"
)

const (
	// AccessControlAllowHeaders : https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Access-Control-Allow-Headers
	AccessControlAllowHeaders = httpapi.AllowedHeaders
	// AccessControlAllowOrigin = "http://localhost:3000"
	// AccessControlAllowOrigin  = "https://mealbot-web.herokuapp.com"
)

// GetCorsHandler : Create handler that sets up CORS policy https://developer.mozilla.org/en-US/docs/Web/HTTP/CORS
func GetCorsHandler(handler http.Handler) http.Handler {
	return httpapi.CORS(handler)
}
