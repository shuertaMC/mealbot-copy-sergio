package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/auth"
)

// Handlers are the seven exact legacy API route slots. Route bodies remain
// independently replaceable while transport assembly moves into this package.
type Handlers struct {
	Members         http.Handler
	Organizations   http.Handler
	Organization    http.Handler
	CrossMatchTrait http.Handler
	Rounds          http.Handler
	Round           http.Handler
	Pairs           http.Handler
}

type route struct {
	pattern  string
	function string
	handler  http.Handler
}

func (h Handlers) routes() []route {
	return []route{
		{pattern: "/members", function: "MembersHandler", handler: h.Members},
		{pattern: "/orgs", function: "GetOrganizationsHandler", handler: h.Organizations},
		{pattern: "/org", function: "CreateOrganizationHandler", handler: h.Organization},
		{pattern: "/crossmatchtrait", function: "CrossMatchTraitHandler", handler: h.CrossMatchTrait},
		{pattern: "/rounds", function: "GetRoundsHandler", handler: h.Rounds},
		{pattern: "/round", function: "RoundHandler", handler: h.Round},
		{pattern: "/pairs", function: "GetPairsHandler", handler: h.Pairs},
	}
}

// RouterConfig supplies every shared HTTP concern explicitly. Auth is injected
// through internal/auth.Middleware; a nil value intentionally means no auth,
// which is useful only for focused compatibility tests.
type RouterConfig struct {
	Handlers        Handlers
	Auth            auth.Middleware
	CORS            CORSConfig
	Logger          *slog.Logger
	StaticDirectory string
}

// NewRouter registers specific API paths before the public root file server.
// For API requests the request logger is outermost, CORS is outside auth, and
// auth is immediately outside the route handler.
func NewRouter(config RouterConfig) *http.ServeMux {
	mux := http.NewServeMux()
	for _, registered := range config.Handlers.routes() {
		handler := registered.handler
		if handler == nil {
			handler = http.NotFoundHandler()
		}
		if config.Auth != nil {
			handler = config.Auth.Protect(handler)
		}
		handler = CORSMiddleware(config.CORS)(handler)
		handler = RequestLogger(config.Logger, registered.function)(handler)
		mux.Handle(registered.pattern, handler)
	}
	RegisterStatic(mux, config.StaticDirectory)
	return mux
}

// NewCompatibilityRouter applies an already composed legacy middleware stack
// to the same route slots. It is an explicit bridge, not a dependency of the
// modern router.
func NewCompatibilityRouter(handlers Handlers, middleware Middleware, staticDirectory string) *http.ServeMux {
	mux := http.NewServeMux()
	for _, registered := range handlers.routes() {
		handler := registered.handler
		if handler == nil {
			handler = http.NotFoundHandler()
		}
		mux.Handle(registered.pattern, middleware.ApplyHandler(handler))
	}
	RegisterStatic(mux, staticDirectory)
	return mux
}
