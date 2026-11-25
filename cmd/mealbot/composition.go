package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	"github.com/johnamadeo/mealbot/internal/auth"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/httpapi"
	"github.com/johnamadeo/mealbot/internal/mailer"
	"github.com/johnamadeo/mealbot/internal/pairing"
	"github.com/johnamadeo/mealbot/internal/store"
)

const (
	staticDirectory       = "./static"
	readHeaderTimeout     = 10 * time.Second
	readTimeout           = 30 * time.Second
	writeTimeout          = 30 * time.Second
	idleTimeout           = 2 * time.Minute
	serverShutdownTimeout = 10 * time.Second
)

// applicationStore is the complete persistence surface consumed by the
// composition root. Each internal package continues to depend on its own
// narrower interface.
type applicationStore interface {
	httpapi.OrganizationStore
	httpapi.MemberStore
	httpapi.RoundStore
	httpapi.PairStore
	pairing.SchedulerStore
	pairing.MemberLoader
	pairing.RoundStore
	ReconstructPairHistory(context.Context) (store.HistoryReconstructionResult, error)
}

type dependencies struct {
	getenv          func(string) string
	stdout          io.Writer
	logger          *slog.Logger
	openDatabase    func(context.Context, string) (*sql.DB, error)
	closeDatabase   func(*sql.DB) error
	applyMigrations func(context.Context, *sql.DB) error
	newStore        func(*sql.DB) (applicationStore, error)
	buildWebHandler func(config.Config, applicationStore, *slog.Logger, string) (http.Handler, error)
	newMailer       func(config.Config) (pairing.Notifier, error)
	listenAndServe  func(*http.Server) error
	clock           pairing.Clock
	random          pairing.Random
	staticDirectory string
}

// Run executes one command using an immutable environment snapshot. It is the
// testable process boundary: main owns only signals, streams, and exit status.
func Run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	deps := productionDependencies()
	deps.getenv = getenv
	deps.stdout = stdout
	return run(ctx, args, deps)
}

func productionDependencies() dependencies {
	logger := defaultLogger()
	return dependencies{
		getenv: func(string) string { return "" },
		stdout: io.Discard,
		logger: logger,
		openDatabase: func(ctx context.Context, databaseURL string) (*sql.DB, error) {
			return store.Open(ctx, databaseURL)
		},
		closeDatabase: func(db *sql.DB) error {
			if db == nil {
				return nil
			}
			return db.Close()
		},
		applyMigrations: func(ctx context.Context, db *sql.DB) error {
			migrator, err := store.NewMigrator(db)
			if err != nil {
				return err
			}
			return migrator.Apply(ctx)
		},
		newStore: func(db *sql.DB) (applicationStore, error) {
			return store.New(db)
		},
		buildWebHandler: buildWebHandler,
		newMailer: func(cfg config.Config) (pairing.Notifier, error) {
			return mailer.New(mailer.Config{
				Domain:    cfg.MailgunDomain,
				APIKey:    cfg.MailgunAPIKey,
				SMTPLogin: cfg.MailgunSMTPLogin,
			})
		},
		listenAndServe:  func(server *http.Server) error { return server.ListenAndServe() },
		clock:           utcClock{},
		random:          pairing.RandomFunc(rand.Intn),
		staticDirectory: staticDirectory,
	}
}

func run(ctx context.Context, args []string, deps dependencies) (resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if deps.getenv == nil {
		deps.getenv = func(string) string { return "" }
	}
	if deps.stdout == nil {
		deps.stdout = io.Discard
	}

	command, unknown, isUnknown := selectedCommand(args)
	if isUnknown {
		_, err := fmt.Fprintf(deps.stdout, "argument '%s' not recognized", unknown)
		return err
	}

	cfg := config.Load(deps.getenv)
	if err := cfg.ValidateForCommand(command); err != nil {
		return fmt.Errorf("validate %s configuration: %w", command, err)
	}

	db, repository, err := openApplicationStore(ctx, cfg, deps)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := deps.closeDatabase(db); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close database: %w", closeErr))
		}
	}()

	switch command {
	case config.CommandWeb:
		return runWeb(ctx, cfg, repository, deps)
	case config.CommandPair:
		return runPair(ctx, cfg, repository, deps)
	case config.CommandMigrate:
		return runMigrate(ctx, repository, deps.stdout)
	default:
		return fmt.Errorf("unsupported command %q", command)
	}
}

// selectedCommand preserves the legacy argument-count behavior. Zero user
// arguments and more than one user argument both select web; exactly one user
// argument is interpreted as a command.
func selectedCommand(args []string) (command, unknown string, isUnknown bool) {
	if len(args) != 1 {
		return config.CommandWeb, "", false
	}
	switch args[0] {
	case config.CommandPair, config.CommandMigrate:
		return args[0], "", false
	default:
		return "", args[0], true
	}
}

func openApplicationStore(ctx context.Context, cfg config.Config, deps dependencies) (*sql.DB, applicationStore, error) {
	if deps.openDatabase == nil || deps.closeDatabase == nil || deps.applyMigrations == nil || deps.newStore == nil {
		return nil, nil, errors.New("database composition is incomplete")
	}
	db, err := deps.openDatabase(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	closeOnError := func(cause error) (*sql.DB, applicationStore, error) {
		if closeErr := deps.closeDatabase(db); closeErr != nil {
			return nil, nil, errors.Join(cause, fmt.Errorf("close database: %w", closeErr))
		}
		return nil, nil, cause
	}
	if err := deps.applyMigrations(ctx, db); err != nil {
		return closeOnError(fmt.Errorf("apply schema migrations: %w", err))
	}
	repository, err := deps.newStore(db)
	if err != nil {
		return closeOnError(fmt.Errorf("construct store: %w", err))
	}
	if repository == nil {
		return closeOnError(errors.New("construct store: nil store"))
	}
	return db, repository, nil
}

func runWeb(ctx context.Context, cfg config.Config, repository applicationStore, deps dependencies) error {
	if deps.buildWebHandler == nil || deps.listenAndServe == nil {
		return errors.New("web composition is incomplete")
	}
	directory := deps.staticDirectory
	if directory == "" {
		directory = staticDirectory
	}
	handler, err := deps.buildWebHandler(cfg, repository, deps.logger, directory)
	if err != nil {
		return fmt.Errorf("construct web handler: %w", err)
	}
	server := &http.Server{
		Addr:              cfg.Address(),
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	if err := serveHTTP(ctx, server, deps.listenAndServe); err != nil {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

func buildWebHandler(cfg config.Config, repository applicationStore, logger *slog.Logger, directory string) (http.Handler, error) {
	keys, err := auth.NewJWKSProvider(auth.JWKSConfig{URL: cfg.AuthJWKSURL})
	if err != nil {
		return nil, err
	}
	validator, err := auth.NewValidator(auth.ValidatorConfig{
		Issuer:   cfg.AuthIssuer,
		Audience: cfg.AuthAudience,
		Keys:     keys,
	})
	if err != nil {
		return nil, err
	}

	responder := httpapi.NewResponder(logger)
	organizations := httpapi.NewOrganizationHandlers(repository, responder)
	members := httpapi.NewMemberHandlers(repository, repository, responder)
	rounds := httpapi.NewRoundHandlers(repository, responder)
	pairs := httpapi.NewPairHandlers(repository, responder)

	return httpapi.NewRouter(httpapi.RouterConfig{
		Handlers: httpapi.Handlers{
			Members:         members,
			Organizations:   http.HandlerFunc(organizations.Organizations),
			Organization:    http.HandlerFunc(organizations.Create),
			CrossMatchTrait: http.HandlerFunc(organizations.CrossMatchTrait),
			Rounds:          http.HandlerFunc(rounds.Get),
			Round:           rounds,
			Pairs:           http.HandlerFunc(pairs.Get),
		},
		Auth: auth.NewMiddleware(validator),
		CORS: httpapi.CORSConfig{
			ReflectOrigin:  cfg.ReflectCORSOrigin,
			AllowedHeaders: cfg.CORSAllowedHeaders,
			AllowedMethods: cfg.CORSAllowedMethods,
		},
		Logger:          logger,
		StaticDirectory: directory,
	}), nil
}

func runPair(ctx context.Context, cfg config.Config, repository applicationStore, deps dependencies) error {
	if deps.clock == nil || deps.random == nil {
		return errors.New("pair composition is incomplete")
	}
	var notifier pairing.Notifier
	if !cfg.TestMode {
		if deps.newMailer == nil {
			return errors.New("pair mail composition is incomplete")
		}
		var err error
		notifier, err = deps.newMailer(cfg)
		if err != nil {
			return fmt.Errorf("construct mailer: %w", err)
		}
	}

	service := pairing.Service{
		Members:           repository,
		Store:             repository,
		Notifier:          notifier,
		Random:            deps.random,
		SkipNotifications: cfg.TestMode,
	}
	scheduler := pairing.Scheduler{
		Store:     repository,
		Clock:     deps.clock,
		Processor: service,
		Observer:  slogRoundObserver(deps.logger),
	}
	return scheduler.Run(ctx)
}

func slogRoundObserver(logger *slog.Logger) pairing.Observer {
	if logger == nil {
		return nil
	}
	return pairing.ObserverFunc(func(_ context.Context, round pairing.ScheduledRound, event pairing.RoundEvent) {
		logger.Info("pairing round",
			"organization", round.Organization,
			"round", round.Number,
			"event", string(event),
		)
	})
}

func runMigrate(ctx context.Context, repository applicationStore, out io.Writer) error {
	result, err := repository.ReconstructPairHistory(ctx)
	if err != nil {
		return fmt.Errorf("reconstruct pair history: %w", err)
	}
	if !result.Completed {
		return errors.New("pair-history reconstruction did not complete")
	}
	_, err = fmt.Fprintln(out, "Done migrating to 'last round with' data structure in pairing algorithm!")
	return err
}

type utcClock struct{}

func (utcClock) Now() time.Time { return time.Now().UTC() }

func serveHTTP(ctx context.Context, server *http.Server, listen func(*http.Server) error) error {
	result := make(chan error, 1)
	go func() { result <- listen(server) }()

	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		err := <-result
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
