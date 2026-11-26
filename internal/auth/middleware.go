package auth

import (
	"context"
	"net/http"
	"strings"
)

const invalidTokenResponse = "{\"error\":\"invalid access token\"}\n"

// TokenValidator is the narrow authentication boundary required by the HTTP
// middleware.
type TokenValidator interface {
	Validate(ctx context.Context, token string) error
}

// Middleware authenticates bearer credentials before delegating requests.
// OPTIONS requests are always delegated so an outer CORS middleware can
// short-circuit valid preflight traffic without credentials.
type Middleware struct {
	validator TokenValidator
}

// NewMiddleware constructs authentication middleware around validator. A nil
// validator produces deterministic unauthorized responses rather than a panic.
func NewMiddleware(validator TokenValidator) Middleware {
	return Middleware{validator: validator}
}

// Handler wraps next with strict Authorization parsing and token validation.
// The accepted form is exactly one header with a case-insensitive Bearer
// scheme, one ASCII space, and a non-empty whitespace-free token.
func (m Middleware) Handler(next http.Handler) http.Handler {
	if next == nil {
		next = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		token, ok := bearerToken(r.Header.Values("Authorization"))
		if !ok || m.validator == nil || m.validator.Validate(r.Context(), token) != nil {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	const prefix = "Bearer "
	value := values[0]
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", false
	}
	token := value[len(prefix):]
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", false
	}
	return token, true
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(invalidTokenResponse))
}
