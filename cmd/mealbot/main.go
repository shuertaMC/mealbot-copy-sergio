// Command mealbot is the Mealbot service entrypoint.
//
//	mealbot          start the HTTP server (PORT, default 8080)
//	mealbot pair     run all scheduled, not yet completed pairing rounds
//	mealbot migrate  backfill the 'last round with' data for all organizations
package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"

	log "github.com/sirupsen/logrus"

	"github.com/johnamadeo/mealbot/internal/auth"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/db"
	"github.com/johnamadeo/mealbot/internal/httputil"
	"github.com/johnamadeo/mealbot/internal/members"
	"github.com/johnamadeo/mealbot/internal/org"
	"github.com/johnamadeo/mealbot/internal/pairs"
	"github.com/johnamadeo/mealbot/internal/rounds"
)

func main() {
	cfg := config.Load()

	args := os.Args
	if len(args) == 2 {
		switch args[1] {
		case "pair":
			runPair(cfg)
		case "migrate":
			runMigrate(cfg)
		default:
			fmt.Printf("argument '%s' not recognized", args[1])
		}
		return
	}

	conn, err := db.Open(cfg.DB)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	log.Fatal(http.ListenAndServe(":"+cfg.Port, newServeMux(cfg, conn)))
}

// newServeMux wires the routes to their handlers.
func newServeMux(cfg config.Config, conn db.Querier) *http.ServeMux {
	mw := httputil.Middleware{
		MiddlewareHandlers: [](func(handler http.Handler) http.Handler){
			auth.NewAuthHandler(cfg.Auth0),
			httputil.GetCorsHandler,
		},
	}

	membersHandler := members.NewHandler(conn)
	orgHandler := org.NewHandler(conn)
	roundsHandler := rounds.NewHandler(conn)
	pairsHandler := pairs.NewHandler(conn)

	serveMux := http.NewServeMux()
	serveMux.Handle("/members", mw.Apply(membersHandler.MembersHandler))
	serveMux.Handle("/orgs", mw.Apply(orgHandler.GetOrganizationsHandler))
	serveMux.Handle("/org", mw.Apply(orgHandler.CreateOrganizationHandler))
	serveMux.Handle("/crossmatchtrait", mw.Apply(orgHandler.CrossMatchTraitHandler))
	serveMux.Handle("/rounds", mw.Apply(roundsHandler.GetRoundsHandler))
	serveMux.Handle("/round", mw.Apply(roundsHandler.RoundHandler))
	serveMux.Handle("/pairs", mw.Apply(pairsHandler.GetPairsHandler))
	serveMux.Handle("/", http.FileServer(http.Dir("./static")))
	return serveMux
}

// openOrReport opens the database for a CLI command, printing (not returning)
// any error like the original implementation.
func openOrReport(cfg config.Config) *sql.DB {
	conn, err := db.Open(cfg.DB)
	if err != nil {
		fmt.Println(err)
		return nil
	}
	return conn
}

func runPair(cfg config.Config) {
	conn := openOrReport(cfg)
	if conn == nil {
		return
	}
	defer conn.Close()

	runner := rounds.NewRunner(conn, rounds.NewMailgunMailer(cfg.Mailgun))
	err := runner.RunPairingScheduler(false)
	if err != nil {
		fmt.Println(err)
	}
}

func runMigrate(cfg config.Config) {
	conn := openOrReport(cfg)
	if conn == nil {
		return
	}
	defer conn.Close()

	err := migrateToLastRoundWithForPairing(conn)
	if err != nil {
		fmt.Println(err)
	}
}
