// Package httpapi wires the HTTP routes, middleware and server lifecycle.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/johnamadeo/mealbot/internal/auth"
	"github.com/johnamadeo/mealbot/internal/httpapi/handlers"
)

// Server timeouts.
const (
	ReadHeaderTimeout = 10 * time.Second
	ReadTimeout       = 30 * time.Second
	WriteTimeout      = 60 * time.Second
	IdleTimeout       = 120 * time.Second
	ShutdownTimeout   = 15 * time.Second
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain wraps core with the middlewares so that the last middleware in the
// list is the outermost one (same ordering as the original Middleware.Apply).
func Chain(core http.Handler, mws ...Middleware) http.Handler {
	handler := core
	for _, mw := range mws {
		handler = mw(handler)
	}
	return handler
}

// Health responds 200 {"status":"ok"}. It does not touch the database.
func Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// NewRouter builds the application routes. authMW may be nil to disable
// authentication (tests only). CORS is the outermost middleware so that
// preflight requests are answered before authentication.
func NewRouter(h *handlers.Handler, authMW *auth.Middleware, staticDir string) http.Handler {
	protect := func(f http.HandlerFunc) http.Handler {
		mws := []Middleware{}
		if authMW != nil {
			mws = append(mws, authMW.Handler)
		}
		mws = append(mws, handlers.Cors)
		return Chain(f, mws...)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", Health)
	mux.Handle("/members", protect(h.Members))
	mux.Handle("/orgs", protect(h.GetOrganizations))
	mux.Handle("/org", protect(h.CreateOrganization))
	mux.Handle("/crossmatchtrait", protect(h.CrossMatchTrait))
	mux.Handle("/rounds", protect(h.GetRounds))
	mux.Handle("/round", protect(h.Round))
	mux.Handle("/pairs", protect(h.GetPairs))
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	return mux
}

// Serve runs an HTTP server on addr until ctx is cancelled, then shuts it
// down gracefully.
func Serve(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}

	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
