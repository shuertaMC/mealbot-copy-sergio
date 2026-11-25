// Command mealbot is the composition root for the web, pairing, and migration
// modes of the Mealbot service.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	mealbot "github.com/johnamadeo/mealbot"
	"github.com/johnamadeo/mealbot/internal/config"
)

func main() {
	if mealbot.DispatchCommand(os.Args, os.Stdout) {
		return
	}

	cfg := config.Load(os.Getenv)
	server := &http.Server{
		Addr:              cfg.Address(),
		Handler:           mealbot.NewHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Fatal(server.ListenAndServe())
}
