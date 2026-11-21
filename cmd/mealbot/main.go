// Command mealbot runs the Mealbot HTTP server, or one of the CLI
// subcommands: "pair" (run due pairing rounds) and "migrate".
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/johnamadeo/mealbot/internal/auth"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/db"
	"github.com/johnamadeo/mealbot/internal/httpapi"
	"github.com/johnamadeo/mealbot/internal/httpapi/handlers"
	"github.com/johnamadeo/mealbot/internal/logging"
	"github.com/johnamadeo/mealbot/internal/mail"
	"github.com/johnamadeo/mealbot/internal/pairing"
	"github.com/johnamadeo/mealbot/internal/store"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfg := config.Load()
	logging.Setup(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.CreateDBConnection(
		ctx,
		cfg.DatabaseURL,
		db.LocalDBConnection{User: cfg.DBUser, DBName: cfg.DBName},
	)
	if err != nil {
		slog.Error("failed to create database pool", "error", err)
		return 1
	}
	defer pool.Close()

	mailer := mail.NewMailgun(cfg.Mailgun)

	if len(args) == 1 {
		switch args[0] {
		case "pair":
			service := pairing.NewService(store.New(pool), mailer, cfg.Mailgun.SMTPLogin)
			if err := service.RunScheduler(ctx, false); err != nil {
				slog.Error("pairing scheduler failed", "error", err)
				return 1
			}
			return 0
		case "migrate":
			if err := pairing.MigrateToLastRoundWith(ctx, store.New(pool)); err != nil {
				slog.Error("migration failed", "error", err)
				return 1
			}
			return 0
		default:
			fmt.Printf("argument '%s' not recognized\n", args[0])
			return 1
		}
	}

	h := handlers.New(pool, mailer, cfg)
	authMW := auth.NewMiddleware(ctx, auth.Config{
		Issuer:   cfg.Auth0.Issuer,
		Audience: cfg.Auth0.Audience,
		JWKSURL:  cfg.Auth0.JWKSURL,
	})
	router := httpapi.NewRouter(h, authMW, cfg.StaticDir)

	if err := httpapi.Serve(ctx, ":"+cfg.Port, router); err != nil {
		slog.Error("server error", "error", err)
		return 1
	}
	return 0
}
