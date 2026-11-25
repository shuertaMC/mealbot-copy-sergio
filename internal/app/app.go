// Package app contains the Mealbot composition root and process lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/johnamadeo/mealbot/internal/auth"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/httpapi"
	"github.com/johnamadeo/mealbot/internal/mailer"
	"github.com/johnamadeo/mealbot/internal/pairing"
	"github.com/johnamadeo/mealbot/internal/store/postgres"
)

// ErrNilContext reports an invalid application lifecycle context.
var ErrNilContext = errors.New("mealbot: nil application context")

// Command identifies one supported process mode.
type Command string

const (
	// CommandServe starts the HTTP service.
	CommandServe Command = "serve"
	// CommandPair processes currently due pairing rounds.
	CommandPair Command = "pair"
	// CommandMigrate reconstructs persisted pairing histories.
	CommandMigrate Command = "migrate"
)

// Repository is the concrete composition surface shared by HTTP, scheduler,
// migration, and lifecycle code. Consumer packages retain narrower contracts.
type Repository interface {
	httpapi.OrganizationRepository
	httpapi.MemberRepository
	httpapi.RoundRepository
	httpapi.PairHistoryRepository
	pairing.DueRoundClaimer
	pairing.PairingPersister
	Migrate(context.Context) error
	ReconstructHistory(context.Context) error
	Close() error
}

// Scheduler runs all currently due pairing rounds.
type Scheduler interface {
	Run(context.Context) error
}

// HTTPServer is the graceful lifecycle surface implemented by http.Server.
type HTTPServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

// Factories defines every external construction seam used by Execute. Tests
// can replace these functions without process-global state or live services.
type Factories struct {
	LoadConfig       func() (config.Config, error)
	Logger           func() *slog.Logger
	OpenRepository   func(context.Context, config.Database) (Repository, error)
	NewDelivery      func(config.Mailgun, time.Duration) (mailer.Delivery, error)
	NewAuthenticator func(config.Auth0, time.Duration) (httpapi.Middleware, error)
	NewScheduler     func(Repository, mailer.Delivery, config.Config) (Scheduler, error)
	NewServer        func(config.Config, Repository, httpapi.Middleware, *slog.Logger) (HTTPServer, error)
}

// ParseArgs parses the argument slice independently of process globals.
func ParseArgs(args []string) (Command, error) {
	if len(args) == 0 {
		return CommandServe, nil
	}
	if len(args) == 1 {
		switch args[0] {
		case string(CommandPair):
			return CommandPair, nil
		case string(CommandMigrate):
			return CommandMigrate, nil
		}
	}
	return "", fmt.Errorf("argument '%s' not recognized", strings.Join(args, " "))
}

// Run starts the production application using os.Args and environment-backed
// configuration. RunArgs is preferred by command tests.
func Run(ctx context.Context) error { return RunArgs(ctx, os.Args[1:]) }

// RunArgs starts the production application for an explicit argument slice.
func RunArgs(ctx context.Context, args []string) error {
	return Execute(ctx, args, ProductionFactories())
}

// Execute loads configuration once, opens and migrates one repository, then
// runs the selected mode. Successfully opened repositories are closed once.
func Execute(ctx context.Context, args []string, factories Factories) (result error) {
	if ctx == nil {
		return ErrNilContext
	}
	command, err := ParseArgs(args)
	if err != nil {
		return err
	}
	if err := validateFactories(factories, command); err != nil {
		return err
	}
	configuration, err := factories.LoadConfig()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	repository, err := factories.OpenRepository(ctx, configuration.Database())
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}
	if repository == nil {
		return errors.New("open repository: factory returned nil")
	}
	defer func() {
		if closeErr := repository.Close(); closeErr != nil {
			result = errors.Join(result, fmt.Errorf("close repository: %w", closeErr))
		}
	}()
	if err := repository.Migrate(ctx); err != nil {
		return fmt.Errorf("apply schema migrations: %w", err)
	}

	switch command {
	case CommandPair:
		delivery, err := factories.NewDelivery(configuration.Mailgun(), configuration.MailTimeout())
		if err != nil {
			return fmt.Errorf("configure mail delivery: %w", err)
		}
		scheduler, err := factories.NewScheduler(repository, delivery, configuration)
		if err != nil {
			return fmt.Errorf("configure pairing scheduler: %w", err)
		}
		if err := scheduler.Run(ctx); err != nil {
			return fmt.Errorf("run pairing scheduler: %w", err)
		}
		return nil
	case CommandMigrate:
		if err := repository.ReconstructHistory(ctx); err != nil {
			return fmt.Errorf("reconstruct pairing history: %w", err)
		}
		return nil
	default:
		authenticate, err := factories.NewAuthenticator(configuration.Auth0(), configuration.JWKSTimeout())
		if err != nil {
			return fmt.Errorf("configure authentication: %w", err)
		}
		server, err := factories.NewServer(configuration, repository, authenticate, factories.Logger())
		if err != nil {
			return fmt.Errorf("configure HTTP server: %w", err)
		}
		if server == nil {
			return errors.New("configure HTTP server: factory returned nil")
		}
		return serve(ctx, server, configuration.RequestTimeout())
	}
}

// ProductionFactories returns the real PostgreSQL, JWT, Mailgun, scheduler,
// router, and HTTP server composition without retaining mutable globals.
func ProductionFactories() Factories {
	randomInt := newRandomInt(uint64(time.Now().UnixNano()), uint64(os.Getpid()))
	clock := pairing.ClockFunc(time.Now)
	return Factories{
		LoadConfig: config.Load,
		Logger: func() *slog.Logger {
			return slog.New(slog.NewJSONHandler(os.Stderr, nil))
		},
		OpenRepository: func(ctx context.Context, database config.Database) (Repository, error) {
			return postgres.Open(ctx, database)
		},
		NewDelivery: func(settings config.Mailgun, timeout time.Duration) (mailer.Delivery, error) {
			return mailer.NewMailgun(mailer.MailgunConfig{Domain: settings.Domain, APIKey: settings.APIKey, Timeout: timeout})
		},
		NewAuthenticator: func(settings config.Auth0, timeout time.Duration) (httpapi.Middleware, error) {
			client := &http.Client{Timeout: timeout}
			keys, err := auth.NewJWKSKeySource(client, settings.JWKSURL, timeout)
			if err != nil {
				return nil, err
			}
			validator, err := auth.NewValidator(settings.Issuer, settings.Audience, keys)
			if err != nil {
				return nil, err
			}
			middleware := auth.NewMiddleware(validator)
			return middleware.Handler, nil
		},
		NewScheduler: func(repository Repository, delivery mailer.Delivery, configuration config.Config) (Scheduler, error) {
			return pairing.NewScheduler(pairing.SchedulerConfig{
				Members: repository, Traits: repository, Rounds: repository,
				Persistence: repository, Delivery: delivery, Clock: clock,
				RandomInt: randomInt, SMTPLogin: configuration.Mailgun().SMTPLogin,
			})
		},
		NewServer: newHTTPServer,
	}
}

func newHTTPServer(configuration config.Config, repository Repository, authenticate httpapi.Middleware, logger *slog.Logger) (HTTPServer, error) {
	organizations := httpapi.NewOrganizationService(repository)
	members := httpapi.NewMemberService(repository)
	rounds := httpapi.NewRoundService(repository)
	pairs := httpapi.NewPairHistoryService(repository)
	handler, err := httpapi.NewRouter(httpapi.RouterConfig{
		Organizations: organizations, Members: members, Rounds: rounds, Pairs: pairs,
		Authenticate: authenticate, CORS: httpapi.NewCORS(configuration.AllowedCORSOrigins()),
		Logger: logger, StaticDir: configuration.StaticDir(), RequestTimeout: configuration.RequestTimeout(),
		MaxBodyBytes: httpapi.DefaultMaxRequestBytes,
	})
	if err != nil {
		return nil, err
	}
	timeout := configuration.RequestTimeout()
	return &http.Server{
		Addr: fmt.Sprintf(":%d", configuration.Port()), Handler: handler,
		ReadHeaderTimeout: timeout, ReadTimeout: timeout, WriteTimeout: timeout, IdleTimeout: 2 * timeout,
	}, nil
}

func serve(ctx context.Context, server HTTPServer, shutdownTimeout time.Duration) error {
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.ListenAndServe() }()
	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownContext)
		serveErr := <-serveErrors
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, serveErr)
	}
}

func validateFactories(factories Factories, command Command) error {
	if factories.LoadConfig == nil || factories.OpenRepository == nil {
		return errors.New("mealbot: configuration and repository factories are required")
	}
	switch command {
	case CommandPair:
		if factories.NewDelivery == nil || factories.NewScheduler == nil {
			return errors.New("mealbot: pairing factories are required")
		}
	case CommandServe:
		if factories.Logger == nil || factories.NewAuthenticator == nil || factories.NewServer == nil {
			return errors.New("mealbot: HTTP server factories are required")
		}
	}
	return nil
}

func newRandomInt(seed1, seed2 uint64) pairing.RandomIntGenerator {
	source := rand.New(rand.NewPCG(seed1, seed2))
	var mutex sync.Mutex
	return func(limit int) int {
		mutex.Lock()
		defer mutex.Unlock()
		return source.IntN(limit)
	}
}
