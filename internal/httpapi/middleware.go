package httpapi

import (
	"log/slog"
	"net/http"
)

// Middleware preserves the legacy composition shape for temporary adapters.
// Handlers are wrapped in slice order, so the final entry executes outermost.
type Middleware struct {
	MiddlewareHandlers []func(http.Handler) http.Handler
}

// Apply wraps a route function in the configured middleware.
func (mw *Middleware) Apply(coreHandler func(http.ResponseWriter, *http.Request)) http.Handler {
	handler := http.Handler(http.HandlerFunc(coreHandler))
	if mw == nil {
		return handler
	}
	for _, nextHandler := range mw.MiddlewareHandlers {
		if nextHandler != nil {
			handler = nextHandler(handler)
		}
	}
	return handler
}

// ApplyHandler wraps an existing http.Handler in the configured middleware.
func (mw Middleware) ApplyHandler(handler http.Handler) http.Handler {
	for _, nextHandler := range mw.MiddlewareHandlers {
		if nextHandler != nil {
			handler = nextHandler(handler)
		}
	}
	return handler
}

// ApplyFake retains the legacy test seam that bypasses all middleware.
func (mw Middleware) ApplyFake(coreHandler func(http.ResponseWriter, *http.Request)) http.Handler {
	return http.HandlerFunc(coreHandler)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

// RequestLogger records only method, function, and status. It deliberately
// excludes headers, query strings, bodies, error text, and timing values so
// credentials and personal/member data cannot enter transport logs.
func RequestLogger(logger *slog.Logger, function string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if logger == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(recorder, r)
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.Info("http request", "function", function, "status", status, "method", r.Method)
		})
	}
}
