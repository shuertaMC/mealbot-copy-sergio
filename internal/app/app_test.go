package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/johnamadeo/mealbot/internal/auth"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/domain"
	"github.com/johnamadeo/mealbot/internal/httpapi"
	"github.com/johnamadeo/mealbot/internal/mailer"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	configuration, err := config.New(config.Values{
		DBUser: "test", DBName: "mealbot", StaticDir: filepath.Join("..", "..", "static"),
		Auth0Issuer: "https://issuer.example/", Auth0Audience: "mealbot", Auth0JWKSURL: "https://issuer.example/jwks",
		MailgunSMTPLogin: "bot@example.com", MailgunDomain: "example.com", MailgunAPIKey: "key",
		AllowedCORSOrigins: []string{"https://web.example"}, RequestTimeout: time.Second, JWKSTimeout: time.Second, MailTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return configuration
}

type fakeRepository struct {
	mutex           sync.Mutex
	organizations   []domain.Organization
	migrateErr      error
	reconstructErr  error
	closeErr        error
	migrations      int
	reconstructions int
	closes          int
}

func (r *fakeRepository) ListOrganizations(_ context.Context, admin string) ([]domain.Organization, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	result := []domain.Organization{}
	for _, organization := range r.organizations {
		if organization.Admin == admin {
			result = append(result, organization)
		}
	}
	return result, nil
}
func (r *fakeRepository) CreateOrganization(_ context.Context, organization domain.Organization) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.organizations = append(r.organizations, organization)
	return nil
}
func (*fakeRepository) UpdateCrossMatchTrait(context.Context, string, *string) error { return nil }
func (*fakeRepository) ListMembers(context.Context, string, bool) ([]domain.Member, error) {
	return []domain.Member{}, nil
}
func (*fakeRepository) ReconcileMembers(_ context.Context, _ string, members []domain.Member) ([]domain.Member, error) {
	return members, nil
}
func (*fakeRepository) CrossMatchTrait(context.Context, string) (*string, error) { return nil, nil }
func (*fakeRepository) ListRounds(context.Context, string) ([]domain.Round, error) {
	return []domain.Round{}, nil
}
func (*fakeRepository) AddRound(_ context.Context, organization string, at time.Time) (domain.Round, error) {
	return domain.Round{Organization: organization, ScheduledAt: at}, nil
}
func (*fakeRepository) RescheduleRound(context.Context, string, int, time.Time) error { return nil }
func (*fakeRepository) DeleteRound(context.Context, string, int) error                { return nil }
func (*fakeRepository) ListPairs(context.Context, string) ([]domain.Round, error) {
	return []domain.Round{}, nil
}
func (*fakeRepository) ClaimDueRounds(context.Context, time.Time, time.Time, int) ([]domain.Round, error) {
	return []domain.Round{}, nil
}
func (*fakeRepository) ReleaseRoundClaim(context.Context, string, int, string) error { return nil }
func (*fakeRepository) PersistPairings(context.Context, string, int, string, []domain.Pair) error {
	return nil
}
func (r *fakeRepository) Migrate(context.Context) error { r.migrations++; return r.migrateErr }
func (r *fakeRepository) ReconstructHistory(context.Context) error {
	r.reconstructions++
	return r.reconstructErr
}
func (r *fakeRepository) Close() error { r.closes++; return r.closeErr }

type fakeDelivery struct{}

func (fakeDelivery) Deliver(context.Context, mailer.Message) error { return nil }

type localKeySource struct {
	key *rsa.PublicKey
}

func (s localKeySource) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if kid != "app-test-key" {
		return nil, errors.New("unknown test key")
	}
	return s.key, nil
}

type fakeScheduler struct {
	calls int
	err   error
}

func (s *fakeScheduler) Run(context.Context) error { s.calls++; return s.err }

type fakeServer struct {
	started     chan struct{}
	stop        chan struct{}
	once        sync.Once
	serveErr    error
	shutdownErr error
	closeErr    error
	shutdowns   int
	closes      int
}

func newFakeServer() *fakeServer {
	return &fakeServer{started: make(chan struct{}), stop: make(chan struct{})}
}
func (s *fakeServer) ListenAndServe() error {
	close(s.started)
	if s.serveErr != nil {
		return s.serveErr
	}
	<-s.stop
	return http.ErrServerClosed
}
func (s *fakeServer) Shutdown(context.Context) error {
	s.shutdowns++
	if s.shutdownErr != nil {
		return s.shutdownErr
	}
	s.once.Do(func() { close(s.stop) })
	return nil
}
func (s *fakeServer) Close() error {
	s.closes++
	s.once.Do(func() { close(s.stop) })
	return s.closeErr
}

func testFactories(t *testing.T, repository *fakeRepository, scheduler *fakeScheduler, server *fakeServer) Factories {
	t.Helper()
	configuration := testConfig(t)
	return Factories{
		LoadConfig:     func() (config.Config, error) { return configuration, nil },
		Logger:         func() *slog.Logger { return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)) },
		OpenRepository: func(context.Context, config.Database) (Repository, error) { return repository, nil },
		NewDelivery:    func(config.Mailgun, time.Duration) (mailer.Delivery, error) { return fakeDelivery{}, nil },
		NewAuthenticator: func(config.Auth0, time.Duration) (httpapi.Middleware, error) {
			return func(handler http.Handler) http.Handler { return handler }, nil
		},
		NewScheduler: func(Repository, mailer.Delivery, config.Config) (Scheduler, error) { return scheduler, nil },
		NewServer: func(config.Config, Repository, httpapi.Middleware, *slog.Logger) (HTTPServer, error) {
			return server, nil
		},
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		args    []string
		command Command
		message string
	}{
		{nil, CommandServe, ""}, {[]string{"pair"}, CommandPair, ""}, {[]string{"migrate"}, CommandMigrate, ""},
		{[]string{"invalid"}, "", "argument 'invalid' not recognized"}, {[]string{"pair", "extra"}, "", "argument 'pair extra' not recognized"},
	}
	for _, test := range tests {
		command, err := ParseArgs(test.args)
		if command != test.command || (err != nil && err.Error() != test.message) || (err == nil && test.message != "") {
			t.Errorf("ParseArgs(%v) = %q, %v", test.args, command, err)
		}
	}
}

func TestExecutePairMigrateAndGracefulServer(t *testing.T) {
	for _, command := range []string{"pair", "migrate"} {
		repository, scheduler, server := &fakeRepository{}, &fakeScheduler{}, newFakeServer()
		err := Execute(context.Background(), []string{command}, testFactories(t, repository, scheduler, server))
		if err != nil || repository.migrations != 1 || repository.closes != 1 {
			t.Fatalf("Execute(%s) = %v, repository %#v", command, err, repository)
		}
		if command == "pair" && scheduler.calls != 1 {
			t.Errorf("pair scheduler calls = %d", scheduler.calls)
		}
		if command == "migrate" && repository.reconstructions != 1 {
			t.Errorf("migration calls = %d", repository.reconstructions)
		}
	}

	repository, scheduler, server := &fakeRepository{}, &fakeScheduler{}, newFakeServer()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Execute(ctx, nil, testFactories(t, repository, scheduler, server)) }()
	<-server.started
	cancel()
	if err := <-done; err != nil || server.shutdowns != 1 || repository.closes != 1 {
		t.Fatalf("graceful Execute() = %v, shutdowns %d closes %d", err, server.shutdowns, repository.closes)
	}
}

func TestExecuteFailuresAreSurfacedAndCloseOnce(t *testing.T) {
	if !errors.Is(Execute(nil, nil, Factories{}), ErrNilContext) {
		t.Fatal("nil context was accepted")
	}
	if err := Execute(context.Background(), nil, Factories{}); err == nil {
		t.Fatal("missing factories were accepted")
	}
	called := false
	factories := testFactories(t, &fakeRepository{}, &fakeScheduler{}, newFakeServer())
	factories.LoadConfig = func() (config.Config, error) { called = true; return config.Config{}, errors.New("bad config") }
	if err := Execute(context.Background(), nil, factories); err == nil || !called || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("config failure = %v", err)
	}

	repository := &fakeRepository{migrateErr: errors.New("migration failed"), closeErr: errors.New("close failed")}
	err := Execute(context.Background(), []string{"migrate"}, testFactories(t, repository, &fakeScheduler{}, newFakeServer()))
	if err == nil || !strings.Contains(err.Error(), "migration failed") || !strings.Contains(err.Error(), "close failed") || repository.closes != 1 {
		t.Fatalf("migration/close failure = %v, closes %d", err, repository.closes)
	}

	for _, command := range []string{"pair", "migrate"} {
		repository := &fakeRepository{reconstructErr: errors.New("reconstruct failed")}
		scheduler := &fakeScheduler{err: errors.New("scheduler failed")}
		err := Execute(context.Background(), []string{command}, testFactories(t, repository, scheduler, newFakeServer()))
		if err == nil || repository.closes != 1 {
			t.Errorf("%s failure = %v, closes %d", command, err, repository.closes)
		}
	}
}

func TestExecuteForceClosesServerAfterGracefulShutdownFailure(t *testing.T) {
	shutdownErr := errors.New("graceful shutdown failed")
	closeErr := errors.New("forced close failed")
	repository := &fakeRepository{}
	server := newFakeServer()
	server.shutdownErr = shutdownErr
	server.closeErr = closeErr
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Execute(ctx, nil, testFactories(t, repository, &fakeScheduler{}, server))
	if !errors.Is(err, shutdownErr) || !errors.Is(err, closeErr) {
		t.Fatalf("Execute() error = %v, want shutdown and close errors", err)
	}
	if server.shutdowns != 1 || server.closes != 1 || repository.closes != 1 {
		t.Fatalf("lifecycle calls = shutdowns %d, server closes %d, repository closes %d", server.shutdowns, server.closes, repository.closes)
	}
}

func TestExecuteRejectsPartialProviderConfigBeforeRepositorySetup(t *testing.T) {
	repository := &fakeRepository{}
	factories := testFactories(t, repository, &fakeScheduler{}, newFakeServer())
	factories.LoadConfig = func() (config.Config, error) {
		return config.FromMap(map[string]string{
			"DB_USER":      "local-user",
			"DB_NAME":      "local-name",
			"AUTH0_ISSUER": "https://tenant.example.test/",
		})
	}
	openCalls := 0
	factories.OpenRepository = func(context.Context, config.Database) (Repository, error) {
		openCalls++
		return repository, nil
	}
	adapterCalls := 0
	factories.NewAuthenticator = func(config.Auth0, time.Duration) (httpapi.Middleware, error) {
		adapterCalls++
		return func(handler http.Handler) http.Handler { return handler }, nil
	}

	err := Execute(context.Background(), nil, factories)
	if err == nil || !strings.Contains(err.Error(), "AUTH0_ISSUER, AUTH0_AUDIENCE, and AUTH0_JWKS_URL must be set together") {
		t.Fatalf("Execute() error = %v", err)
	}
	if openCalls != 0 || repository.migrations != 0 || adapterCalls != 0 {
		t.Fatalf("startup side effects = opens %d, migrations %d, adapters %d", openCalls, repository.migrations, adapterCalls)
	}
}

func TestComposedHTTPServerAuthenticatedOrganizationSmokeAndStatic(t *testing.T) {
	repository := &fakeRepository{}
	configuration := testConfig(t)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	settings := configuration.Auth0()
	validator, err := auth.NewValidator(settings.Issuer, settings.Audience, localKeySource{key: &privateKey.PublicKey})
	if err != nil {
		t.Fatal(err)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer: settings.Issuer, Audience: jwt.ClaimStrings{settings.Audience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	token.Header["kid"] = "app-test-key"
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	composed, err := newHTTPServer(configuration, repository, auth.NewMiddleware(validator).Handler, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	server := composed.(*http.Server)
	if server.Addr != ":8080" || server.ReadTimeout != time.Second || server.IdleTimeout != 2*time.Second {
		t.Fatalf("server configuration = %#v", server)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown composed server: %v", err)
		}
		if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve composed server: %v", err)
		}
	})

	client := &http.Client{Timeout: time.Second}
	request := func(method, path, authorization, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+listener.Addr().String()+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		responseBody, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read response: %v; close response: %v", readErr, closeErr)
		}
		return response.StatusCode, string(responseBody)
	}

	authorization := "Bearer " + signedToken
	createStatus, createBody := request(http.MethodPost, "/org?admin=admin@example.com", authorization, `{"org":"club"}`)
	if createStatus != http.StatusCreated || !strings.Contains(createBody, "Successfully created new organization") {
		t.Fatalf("create organization response = %d %q", createStatus, createBody)
	}
	listStatus, listBody := request(http.MethodGet, "/orgs?admin=admin@example.com", authorization, "")
	if listStatus != http.StatusOK || listBody != "{\"orgs\":[\"club\"]}\n" {
		t.Fatalf("list organizations response = %d %q", listStatus, listBody)
	}
	privacyStatus, privacyBody := request(http.MethodGet, "/privacy.html", "", "")
	if privacyStatus != http.StatusOK || !strings.Contains(privacyBody, "<title>Privacy Policy</title>") {
		t.Fatalf("privacy response = %d %q", privacyStatus, privacyBody)
	}
}

func TestProductionFactoriesAndRandomAreConstructible(t *testing.T) {
	factories := ProductionFactories()
	if err := validateFactories(factories, CommandServe); err != nil {
		t.Fatal(err)
	}
	random := newRandomInt(1, 2)
	for range 10 {
		if value := random(3); value < 0 || value >= 3 {
			t.Fatalf("random value = %d", value)
		}
	}
}
