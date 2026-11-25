package mealbot

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"

	internalauth "github.com/johnamadeo/mealbot/internal/auth"
	"github.com/johnamadeo/mealbot/internal/httpapi"
)

// Middleware is the explicit legacy bridge to internal/httpapi composition.
type Middleware = httpapi.Middleware

var runPairingSchedulerForCommand = runPairingScheduler
var migrateForCommand = migrateToLastRoundWithForPairing

// dispatchCommand handles the legacy one-argument command modes. It returns
// true when main should exit instead of starting the web server.
func dispatchCommand(args []string, out io.Writer) bool {
	if len(args) != 2 {
		return false
	}

	switch args[1] {
	case "pair":
		if err := runPairingSchedulerForCommand(false); err != nil {
			fmt.Fprintln(out, err)
		}
	case "migrate":
		if err := migrateForCommand(); err != nil {
			fmt.Fprintln(out, err)
		}
	default:
		fmt.Fprintf(out, "argument '%s' not recognized", args[1])
	}

	return true
}

// DispatchCommand preserves the legacy pair, migrate, and unknown-command
// behavior while command composition moves into cmd/mealbot.
func DispatchCommand(args []string, out io.Writer) bool {
	return dispatchCommand(args, out)
}

func newServeMux(mw Middleware) *http.ServeMux {
	return httpapi.NewCompatibilityRouter(legacyHandlerSlots(), mw, "./static")
}

func legacyHandlerSlots() httpapi.Handlers {
	organizations := legacyOrganizationHandlers()
	members := legacyMemberHandlers()
	rounds := legacyRoundHandlers()
	pairs := legacyPairHandlers()
	return httpapi.Handlers{
		Members:         members,
		Organizations:   http.HandlerFunc(organizations.Organizations),
		Organization:    http.HandlerFunc(organizations.Create),
		CrossMatchTrait: http.HandlerFunc(organizations.CrossMatchTrait),
		Rounds:          http.HandlerFunc(rounds.Get),
		Round:           rounds,
		Pairs:           http.HandlerFunc(pairs.Get),
	}
}

// NewHandler assembles the compatibility HTTP stack for the composition root.
// Static assets remain outside authentication and CORS middleware.
func NewHandler() http.Handler {
	return httpapi.NewRouter(httpapi.RouterConfig{
		Handlers:        legacyHandlerSlots(),
		Auth:            internalauth.MiddlewareFunc(GetAuthHandler),
		CORS:            httpapi.CompatibilityCORS(),
		Logger:          slog.Default(),
		StaticDirectory: "./static",
	})
}
