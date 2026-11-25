package httpapi

import (
	"net/http"
	"strings"
)

const (
	allowMethods = "GET, POST, DELETE, OPTIONS"
	allowHeaders = "Authorization, Content-Type, Origin, Accept, token"
	corsError    = "{\"error\":\"origin not allowed\"}\n"
)

var allowedCORSMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodPost:    {},
	http.MethodDelete:  {},
	http.MethodOptions: {},
}

var allowedCORSHeaders = map[string]struct{}{
	"authorization": {},
	"content-type":  {},
	"origin":        {},
	"accept":        {},
	"token":         {},
}

// CORS enforces an explicit origin allowlist and emits the API's CORS
// contract. It is immutable and safe for concurrent use.
type CORS struct {
	origins map[string]struct{}
}

// NewCORS constructs an allowlist from exact, non-empty origin values. Blank
// entries are ignored and no wildcard behavior is inferred.
func NewCORS(origins []string) CORS {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}
	return CORS{origins: allowed}
}

// Handler wraps next with explicit CORS enforcement. A valid preflight is
// answered immediately, so this middleware must be placed outside auth.
func (c CORS) Handler(next http.Handler) http.Handler {
	if next == nil {
		next = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addVaryOrigin(w.Header())
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := c.origins[origin]; !ok {
			writeCORSForbidden(w)
			return
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", allowMethods)
		w.Header().Set("Access-Control-Allow-Headers", allowHeaders)

		requestedMethod := r.Header.Get("Access-Control-Request-Method")
		if r.Method == http.MethodOptions && requestedMethod != "" {
			if _, ok := allowedCORSMethods[requestedMethod]; !ok || !allowedRequestedHeaders(r.Header.Get("Access-Control-Request-Headers")) {
				writeCORSForbidden(w)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowedRequestedHeaders(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	for _, header := range strings.Split(raw, ",") {
		if _, ok := allowedCORSHeaders[strings.ToLower(strings.TrimSpace(header))]; !ok {
			return false
		}
	}
	return true
}

func addVaryOrigin(header http.Header) {
	for _, value := range header.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "Origin") {
				return
			}
		}
	}
	header.Add("Vary", "Origin")
}

func writeCORSForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(corsError))
}
