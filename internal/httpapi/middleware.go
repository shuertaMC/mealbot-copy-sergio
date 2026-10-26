package httpapi

import "net/http"

// AccessControlAllowHeaders : https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Access-Control-Allow-Headers
const AccessControlAllowHeaders = "Authorization, Content-Type, Origin, Accept, token"

// Middleware wraps handlers with a chain of handler decorators.
type Middleware struct {
	MiddlewareHandlers [](func(handler http.Handler) http.Handler)
}

// Apply creates a handler where the core handler is wrapped by the middleware
// handlers, in list order (the first one listed is applied innermost, so the
// last one listed sees the request first).
func (mw Middleware) Apply(
	coreHandler func(w http.ResponseWriter, r *http.Request),
) http.Handler {
	handler := http.Handler(http.HandlerFunc(coreHandler))
	for _, nextHandler := range mw.MiddlewareHandlers {
		handler = nextHandler(handler)
	}

	return handler
}

// CORS creates a handler that sets up the CORS policy and answers preflight
// requests: https://developer.mozilla.org/en-US/docs/Web/HTTP/CORS
func CORS(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO: Get rid of temporary AccessControlAllowOrigin debugging setup
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Header().Add("Access-Control-Allow-Headers", AccessControlAllowHeaders)
		w.Header().Add("Access-Control-Allow-Methods", "GET, POST, DELETE")

		if r.Method == http.MethodOptions {
			return
		}

		handler.ServeHTTP(w, r)
	})
}
