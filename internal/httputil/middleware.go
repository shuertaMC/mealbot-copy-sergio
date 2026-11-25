package httputil

import "net/http"

// Middleware class
type Middleware struct {
	MiddlewareHandlers [](func(handler http.Handler) http.Handler)
}

// Apply : Create a handler where the core handler is wrapped by middleware handlers
func (mw *Middleware) Apply(
	coreHandler func(w http.ResponseWriter, r *http.Request),
) http.Handler {
	handler := http.Handler(http.HandlerFunc(coreHandler))
	for _, nextHandler := range mw.MiddlewareHandlers {
		handler = nextHandler(handler)
	}

	return handler
}
