package mealbot

import (
	"fmt"
	"io"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/httpapi"
)

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

// ApplyFake :
func (mw Middleware) ApplyFake(
	coreHandler func(w http.ResponseWriter, r *http.Request),
) http.Handler {
	return http.Handler(http.HandlerFunc(coreHandler))
}

func runTestSequence(testMode bool) {
	err := createOrganization("ysc", "johnamadeo.daniswara@yale.edu")
	if err != nil {
		fmt.Println(err)
	}

	_, err = createMembersFromCSV("ysc", "./csv/test_john3.csv")
	if err != nil {
		fmt.Println(err)
		return
	}

	i := 0
	for i < 2 {
		err = addRound("ysc", fmt.Sprintf("2019-01-02 %d:55:00", i))
		if err != nil {
			fmt.Println(err)
			return
		}

		// NOTE: Do you want to actually send out emails?
		err = runPairingRound("ysc", i, testMode)
		if err != nil {
			fmt.Println(err)
			return
		}

		i++
	}
}

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
	serveMux := http.NewServeMux()
	serveMux.Handle("/members", mw.Apply(MembersHandler))
	serveMux.Handle("/orgs", mw.Apply(GetOrganizationsHandler))
	serveMux.Handle("/org", mw.Apply(CreateOrganizationHandler))
	serveMux.Handle("/crossmatchtrait", mw.Apply(CrossMatchTraitHandler))
	serveMux.Handle("/rounds", mw.Apply(GetRoundsHandler))
	serveMux.Handle("/round", mw.Apply(RoundHandler))
	serveMux.Handle("/pairs", mw.Apply(GetPairsHandler))
	httpapi.RegisterStatic(serveMux, "./static")
	return serveMux
}

// NewHandler assembles the compatibility HTTP stack for the composition root.
// Static assets remain outside authentication and CORS middleware.
func NewHandler() http.Handler {
	mw := Middleware{
		MiddlewareHandlers: [](func(handler http.Handler) http.Handler){
			GetAuthHandler,
			GetCorsHandler,
		},
	}

	return newServeMux(mw)
}
