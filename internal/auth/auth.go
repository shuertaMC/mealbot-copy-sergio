// Package auth owns request authentication independently from route handlers.
package auth

import "net/http"

// Middleware protects an HTTP handler. Implementations may validate tokens;
// tests can use MiddlewareFunc to install deterministic fakes.
type Middleware interface {
	Protect(http.Handler) http.Handler
}

// MiddlewareFunc adapts a function to Middleware.
type MiddlewareFunc func(http.Handler) http.Handler

// Protect applies f to next.
func (f MiddlewareFunc) Protect(next http.Handler) http.Handler {
	return f(next)
}
