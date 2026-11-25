package main

import (
	"fmt"
	"io"
	"net/http"
	"os"

	log "github.com/sirupsen/logrus"
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

func newServeMux(mw Middleware) *http.ServeMux {
	serveMux := http.NewServeMux()
	serveMux.Handle("/members", mw.Apply(MembersHandler))
	serveMux.Handle("/orgs", mw.Apply(GetOrganizationsHandler))
	serveMux.Handle("/org", mw.Apply(CreateOrganizationHandler))
	serveMux.Handle("/crossmatchtrait", mw.Apply(CrossMatchTraitHandler))
	serveMux.Handle("/rounds", mw.Apply(GetRoundsHandler))
	serveMux.Handle("/round", mw.Apply(RoundHandler))
	serveMux.Handle("/pairs", mw.Apply(GetPairsHandler))
	serveMux.Handle("/", http.FileServer(http.Dir("./static")))
	return serveMux
}

func main() {
	if dispatchCommand(os.Args, os.Stdout) {
		return
	}

	mw := Middleware{
		MiddlewareHandlers: [](func(handler http.Handler) http.Handler){
			GetAuthHandler,
			GetCorsHandler,
		},
	}

	serveMux := newServeMux(mw)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Fatal(http.ListenAndServe(":"+port, serveMux))
}
