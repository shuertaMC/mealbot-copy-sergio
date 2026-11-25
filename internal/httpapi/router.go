package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// Middleware wraps an HTTP handler.
type Middleware func(http.Handler) http.Handler

// RouterConfig contains all dependencies and policies for the service mux.
type RouterConfig struct {
	Organizations  OrganizationUseCases
	Members        MemberUseCases
	Rounds         RoundUseCases
	Pairs          PairHistoryUseCases
	Authenticate   Middleware
	CORS           CORS
	Logger         *slog.Logger
	StaticDir      string
	RequestTimeout time.Duration
	MaxBodyBytes   int64
}

// NewRouter constructs the complete Mealbot handler. CORS surrounds API auth,
// valid preflights remain credential-free, and static files remain public.
func NewRouter(config RouterConfig) (http.Handler, error) {
	if config.Organizations == nil || config.Members == nil || config.Rounds == nil || config.Pairs == nil {
		return nil, errors.New("httpapi router: all use cases are required")
	}
	if config.Authenticate == nil {
		return nil, errors.New("httpapi router: authentication middleware is required")
	}
	if strings.TrimSpace(config.StaticDir) == "" {
		return nil, errors.New("httpapi router: static directory is required")
	}
	if config.RequestTimeout <= 0 {
		return nil, errors.New("httpapi router: request timeout must be positive")
	}
	if info, err := os.Stat(config.StaticDir); err != nil || !info.IsDir() {
		return nil, errors.New("httpapi router: static directory is unavailable")
	}

	mux := http.NewServeMux()
	registerAPI := func(pattern string, handler http.Handler) { mux.Handle(pattern, config.Authenticate(handler)) }
	registerAPI("/members", NewMembersHandler(config.Members, config.MaxBodyBytes, config.Logger))
	registerAPI("/orgs", NewOrganizationsHandler(config.Organizations, config.Logger))
	registerAPI("/org", NewCreateOrganizationHandler(config.Organizations, config.MaxBodyBytes, config.Logger))
	registerAPI("/crossmatchtrait", NewCrossMatchTraitHandler(config.Organizations, config.MaxBodyBytes, config.Logger))
	registerAPI("/rounds", NewRoundsHandler(config.Rounds, config.Logger))
	registerAPI("/round", NewRoundHandler(config.Rounds, config.Logger))
	registerAPI("/pairs", NewPairsHandler(config.Pairs, config.Logger))
	mux.Handle("/", http.FileServer(http.Dir(config.StaticDir)))

	return config.CORS.Handler(withRequestTimeout(mux, config.RequestTimeout)), nil
}

func withRequestTimeout(next http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, request.WithContext(ctx))
	})
}
