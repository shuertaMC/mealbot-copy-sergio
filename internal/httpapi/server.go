// Package httpapi implements the Mealbot REST API.
package httpapi

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/store"
)

// Server holds the dependencies shared by all HTTP handlers.
type Server struct {
	DB     *sql.DB
	Config config.Config
	Logger *slog.Logger

	store *store.Store
}

// NewServer creates a Server. db may be nil in tests that never reach the database.
func NewServer(db *sql.DB, cfg config.Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		DB:     db,
		Config: cfg,
		Logger: logger,
		store:  store.New(db),
	}
}

// Routes builds the HTTP handler for the whole service. Every API route is
// wrapped by mw; /healthz and the static files are not.
func (s *Server) Routes(mw Middleware) http.Handler {
	mux := http.NewServeMux()

	// route registers handler for its method-aware pattern, and fallback for
	// every other method on the same path (OPTIONS preflights are answered by
	// the CORS middleware before they reach it).
	route := func(path string, allowed []string, handlers map[string]http.HandlerFunc) {
		for method, handler := range handlers {
			mux.Handle(method+" "+path, mw.Apply(handler))
		}
		mux.Handle(path, mw.Apply(s.methodNotAllowed(allowed)))
	}

	route("/members", []string{"GET", "POST"}, map[string]http.HandlerFunc{
		"GET":  s.getMembers,
		"POST": s.createMembers,
	})
	route("/orgs", []string{"GET"}, map[string]http.HandlerFunc{
		"GET": s.getOrganizations,
	})
	route("/org", []string{"POST"}, map[string]http.HandlerFunc{
		"POST": s.createOrganization,
	})
	route("/crossmatchtrait", []string{"POST"}, map[string]http.HandlerFunc{
		"POST": s.setCrossMatchTrait,
	})
	route("/rounds", []string{"GET"}, map[string]http.HandlerFunc{
		"GET": s.getRounds,
	})
	route("/round", []string{"POST", "DELETE"}, map[string]http.HandlerFunc{
		"POST":   s.postRound,
		"DELETE": s.removeRound,
	})
	route("/pairs", []string{"GET"}, map[string]http.HandlerFunc{
		"GET": s.getPairs,
	})

	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("/", http.FileServer(http.Dir(s.Config.StaticDir)))

	return mux
}

// healthz reports that the process is up and serving requests.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok"))
}

// methodNotAllowed answers requests whose method the route does not support.
func (s *Server) methodNotAllowed(allowed []string) http.HandlerFunc {
	var message string
	switch len(allowed) {
	case 1:
		message = "Only " + allowed[0] + " requests are allowed at this route"
	default:
		message = "Only " + allowed[0] + " and " + allowed[1] + " requests are allowed at this route"
	}

	allowHeader := ""
	for i, method := range allowed {
		if i > 0 {
			allowHeader += ", "
		}
		allowHeader += method
	}

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allowHeader)
		s.writeErr(w, errors.New(message), http.StatusMethodNotAllowed, "methodNotAllowed")
	}
}
