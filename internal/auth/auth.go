// Package auth validates Auth0 access tokens and protects HTTP handlers.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

const InvalidAccessToken = "Invalid access token"

// ErrUnauthorized is deliberately generic so callers cannot accidentally
// expose token, claim, or provider details in an HTTP response or log entry.
var ErrUnauthorized = errors.New("authentication failed")

// Middleware protects an HTTP handler. Tests can use MiddlewareFunc to install
// deterministic fakes without constructing tokens.
type Middleware interface {
	Protect(http.Handler) http.Handler
}

// MiddlewareFunc adapts a function to Middleware.
type MiddlewareFunc func(http.Handler) http.Handler

// Protect applies f to next.
func (f MiddlewareFunc) Protect(next http.Handler) http.Handler {
	return f(next)
}

// TokenValidator is the narrow boundary used by the HTTP middleware.
type TokenValidator interface {
	Validate(context.Context, string) error
}

// BearerToken extracts a single bearer credential. Its errors intentionally do
// not distinguish missing, malformed, and empty credentials.
func BearerToken(header string) (string, error) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || fields[1] == "" {
		return "", ErrUnauthorized
	}
	return fields[1], nil
}

// HTTPMiddleware applies a TokenValidator to requests.
type HTTPMiddleware struct {
	validator TokenValidator
}

// NewMiddleware constructs middleware around an injected validator.
func NewMiddleware(validator TokenValidator) *HTTPMiddleware {
	return &HTTPMiddleware{validator: validator}
}

// Protect permits compatibility OPTIONS requests and otherwise returns one
// generic unauthorized response for every authentication failure.
func (m *HTTPMiddleware) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		token, err := BearerToken(r.Header.Get("Authorization"))
		if err != nil || m == nil || m.validator == nil || m.validator.Validate(r.Context(), token) != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, InvalidAccessToken, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
