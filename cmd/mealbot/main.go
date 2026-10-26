// Command mealbot serves the Mealbot REST API and runs its batch jobs.
//
// Usage:
//
//	mealbot           serve the HTTP API on $PORT
//	mealbot pair      run every scheduled round that is due and email the groups
//	mealbot migrate   rebuild the members' 'last round with' history from stored pairs
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/johnamadeo/mealbot/internal/auth"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/httpapi"
	"github.com/johnamadeo/mealbot/internal/logging"
	"github.com/johnamadeo/mealbot/internal/mail"
	"github.com/johnamadeo/mealbot/internal/migration"
	"github.com/johnamadeo/mealbot/internal/pairing"
	"github.com/johnamadeo/mealbot/internal/server"
	"github.com/johnamadeo/mealbot/internal/store"
)

func main() {
	cfg := config.FromEnv()
	logger := logging.New(os.Stderr, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], cfg, logger); err != nil {
		logger.Error("mealbot failed", "error", err)
		stop()
		os.Exit(1)
	}
}

// run executes the mode named by args: no argument serves the API.
func run(ctx context.Context, args []string, cfg config.Config, logger *slog.Logger) error {
	switch {
	case len(args) == 0:
		return serve(ctx, cfg, logger)
	case len(args) == 1 && args[0] == "pair":
		return runPair(ctx, cfg, logger)
	case len(args) == 1 && args[0] == "migrate":
		return runMigrate(ctx, cfg, logger)
	default:
		return fmt.Errorf("argument '%s' not recognized (expected 'pair' or 'migrate', or no argument to serve)", args[0])
	}
}

// openDB connects to the configured database. An unreachable database is only
// reported as a warning so the process can still come up and answer /healthz.
func openDB(ctx context.Context, cfg config.Config, logger *slog.Logger) (*sql.DB, error) {
	if err := cfg.ValidateDatabase(); err != nil {
		return nil, err
	}

	db, err := server.CreateDBConnection(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		logger.Warn("database is not reachable yet", "error", err)
	}

	return db, nil
}

func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	db, err := openDB(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	validator := auth.NewValidator(cfg.Auth0, auth.NewJWKSClient(cfg.Auth0.JWKSURL, nil), logger)
	mw := httpapi.Middleware{
		MiddlewareHandlers: [](func(handler http.Handler) http.Handler){
			validator.Handler,
			httpapi.CORS,
		},
	}

	api := httpapi.NewServer(db, cfg, logger)
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.Routes(mw),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	logger.Info("mealbot listening", "port", cfg.Port)

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func runPair(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	db, err := openDB(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(os.Getpid())))
	runner := pairing.NewRunner(store.New(db), mail.NewMailgun(cfg.Mailgun), logger)
	return runner.RunScheduler(ctx, true, rng)
}

func runMigrate(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	db, err := openDB(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer db.Close()

	return migration.MigrateToLastRoundWith(ctx, store.New(db), logger)
}
