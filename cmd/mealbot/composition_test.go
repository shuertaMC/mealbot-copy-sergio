package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/pairing"
	"github.com/johnamadeo/mealbot/internal/store"
)

func TestSelectedCommandPreservesLegacyArgumentCount(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantCommand   string
		wantUnknown   string
		wantIsUnknown bool
	}{
		{name: "zero arguments serves web", wantCommand: config.CommandWeb},
		{name: "pair", args: []string{"pair"}, wantCommand: config.CommandPair},
		{name: "migrate", args: []string{"migrate"}, wantCommand: config.CommandMigrate},
		{name: "unknown", args: []string{"wat"}, wantUnknown: "wat", wantIsUnknown: true},
		{name: "empty unknown", args: []string{""}, wantUnknown: "", wantIsUnknown: true},
		{name: "extra arguments serve web", args: []string{"pair", "extra"}, wantCommand: config.CommandWeb},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, unknown, isUnknown := selectedCommand(test.args)
			if command != test.wantCommand || unknown != test.wantUnknown || isUnknown != test.wantIsUnknown {
				t.Fatalf("selectedCommand(%q) = (%q, %q, %v), want (%q, %q, %v)", test.args, command, unknown, isUnknown, test.wantCommand, test.wantUnknown, test.wantIsUnknown)
			}
		})
	}
}

func TestRunUnknownCommandWritesExactResponseWithoutExternalWork(t *testing.T) {
	var output bytes.Buffer
	getenvCalls := 0
	openCalls := 0
	deps := dependencies{
		getenv: func(string) string { getenvCalls++; return "" },
		stdout: &output,
		openDatabase: func(context.Context, string) (*sql.DB, error) {
			openCalls++
			return nil, nil
		},
	}
	if err := run(context.Background(), []string{"unknown"}, deps); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "argument 'unknown' not recognized"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if getenvCalls != 0 || openCalls != 0 {
		t.Fatalf("unknown command performed external work: getenv=%d open=%d", getenvCalls, openCalls)
	}
}

func TestRunValidatesSelectedCommandBeforeOpeningDatabase(t *testing.T) {
	secret := "postgres://secret-value@example.invalid/db"
	openCalls := 0
	deps := dependencies{
		getenv: mapEnvironment(map[string]string{"DATABASE_URL": secret}),
		stdout: io.Discard,
		openDatabase: func(context.Context, string) (*sql.DB, error) {
			openCalls++
			return nil, nil
		},
	}
	err := run(context.Background(), []string{"pair"}, deps)
	if err == nil || !strings.Contains(err.Error(), "MAILGUN_DOMAIN") {
		t.Fatalf("run error = %v, want missing Mailgun setting", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("configuration error exposed secret %q", secret)
	}
	if openCalls != 0 {
		t.Fatalf("database opened %d times before validation", openCalls)
	}
}

func TestRunWebAppliesMigrationsBuildsHandlerAndReturnsServerError(t *testing.T) {
	serveErr := errors.New("listener stopped")
	events := make([]string, 0)
	repository := &fakeApplicationStore{}
	deps := baseDependencies(repository)
	deps.getenv = mapEnvironment(map[string]string{
		"DATABASE_URL": "postgres://database",
		"PORT":         "9090",
	})
	deps.openDatabase = func(_ context.Context, databaseURL string) (*sql.DB, error) {
		events = append(events, "open:"+databaseURL)
		return nil, nil
	}
	deps.applyMigrations = func(context.Context, *sql.DB) error {
		events = append(events, "migrate")
		return nil
	}
	deps.newStore = func(*sql.DB) (applicationStore, error) {
		events = append(events, "store")
		return repository, nil
	}
	deps.buildWebHandler = func(_ config.Config, got applicationStore, _ *slog.Logger, directory string) (http.Handler, error) {
		events = append(events, "handler:"+directory)
		if got != repository {
			t.Fatal("handler received a different store")
		}
		return http.NotFoundHandler(), nil
	}
	deps.listenAndServe = func(server *http.Server) error {
		events = append(events, "serve")
		if server.Addr != ":9090" {
			t.Fatalf("server address = %q", server.Addr)
		}
		if server.ReadHeaderTimeout != readHeaderTimeout || server.ReadTimeout != readTimeout || server.WriteTimeout != writeTimeout || server.IdleTimeout != idleTimeout {
			t.Fatalf("unexpected server timeouts: %+v", server)
		}
		return serveErr
	}
	deps.closeDatabase = func(*sql.DB) error {
		events = append(events, "close")
		return nil
	}

	err := run(context.Background(), nil, deps)
	if !errors.Is(err, serveErr) {
		t.Fatalf("run error = %v, want %v", err, serveErr)
	}
	want := []string{"open:postgres://database", "migrate", "store", "handler:./static", "serve", "close"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestBuildWebHandlerServesStaticAndPreservesAuthenticationContractsOnEveryAPIRoute(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "privacy.html"), []byte("privacy page"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load(mapEnvironment(map[string]string{"DATABASE_URL": "postgres://database"}))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repository := &fakeApplicationStore{}
	handler, err := buildWebHandler(cfg, repository, logger, directory)
	if err != nil {
		t.Fatal(err)
	}

	staticRequest := httptest.NewRequest(http.MethodGet, "/privacy.html", nil)
	staticResponse := httptest.NewRecorder()
	handler.ServeHTTP(staticResponse, staticRequest)
	if staticResponse.Code != http.StatusOK || staticResponse.Body.String() != "privacy page" {
		t.Fatalf("static response = %d %q", staticResponse.Code, staticResponse.Body.String())
	}

	routes := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "members", method: http.MethodGet, target: "/members?org=alpha"},
		{name: "organizations", method: http.MethodGet, target: "/orgs?admin=admin"},
		{name: "organization", method: http.MethodPost, target: "/org?admin=admin", body: `{"org":"alpha"}`},
		{name: "cross match trait", method: http.MethodPost, target: "/crossmatchtrait?org=alpha", body: `{"trait":"cohort"}`},
		{name: "rounds", method: http.MethodGet, target: "/rounds?org=alpha"},
		{name: "round", method: http.MethodPost, target: "/round?org=alpha&round=2026-11-25T12%3A00%3A00Z"},
		{name: "pairs", method: http.MethodGet, target: "/pairs?org=alpha"},
	}
	credentials := []struct {
		name   string
		header string
	}{
		{name: "missing"},
		{name: "malformed", header: "Basic token"},
		{name: "unverifiable", header: "Bearer not.a.jwt"},
	}
	for _, route := range routes {
		for _, credential := range credentials {
			t.Run(route.name+"/"+credential.name, func(t *testing.T) {
				apiRequest := httptest.NewRequest(route.method, route.target, strings.NewReader(route.body))
				apiRequest.Header.Set("Authorization", credential.header)
				apiRequest.Header.Set("Origin", "https://client.example")
				apiResponse := httptest.NewRecorder()
				handler.ServeHTTP(apiResponse, apiRequest)

				if apiResponse.Code != http.StatusOK || apiResponse.Body.Len() != 0 {
					t.Fatalf("authentication rejection = %d %q, want empty 200", apiResponse.Code, apiResponse.Body.String())
				}
				wantHeaders := map[string]string{
					"Access-Control-Allow-Origin":  "https://client.example",
					"Access-Control-Allow-Headers": config.DefaultCORSAllowedHeaders,
					"Access-Control-Allow-Methods": config.DefaultCORSAllowedMethods,
				}
				if len(apiResponse.Header()) != len(wantHeaders) {
					t.Fatalf("authentication rejection headers = %v, want only compatibility CORS headers", apiResponse.Header())
				}
				for name, want := range wantHeaders {
					if got := apiResponse.Header().Get(name); got != want {
						t.Fatalf("%s = %q, want %q", name, got, want)
					}
				}
			})
		}
	}
	if repository.httpCalls != 0 {
		t.Fatalf("authentication rejections invoked protected stores %d times", repository.httpCalls)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/orgs", nil)
	preflight.Header.Set("Origin", "https://client.example")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusOK || preflightResponse.Header().Get("Access-Control-Allow-Origin") != "https://client.example" {
		t.Fatalf("preflight response = %d headers=%v", preflightResponse.Code, preflightResponse.Header())
	}
}

func TestRunPairTestModeSkipsMailerAndStillSaves(t *testing.T) {
	events := make([]string, 0)
	repository := pairedStore(&events)
	deps := baseDependencies(repository)
	deps.getenv = mapEnvironment(map[string]string{
		"DATABASE_URL":      "postgres://database",
		"MEALBOT_TEST_MODE": "true",
	})
	deps.newMailer = func(config.Config) (pairing.Notifier, error) {
		t.Fatal("test mode constructed a Mailgun notifier")
		return nil, nil
	}
	if err := run(context.Background(), []string{"pair"}, deps); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"due", "load", "save"}) {
		t.Fatalf("events = %#v", events)
	}
}

func TestRunPairDeliversBeforeSaving(t *testing.T) {
	events := make([]string, 0)
	repository := pairedStore(&events)
	deps := baseDependencies(repository)
	deps.getenv = mapEnvironment(map[string]string{
		"DATABASE_URL":       "postgres://database",
		"MAILGUN_DOMAIN":     "mail.example",
		"MAILGUN_API_KEY":    "key",
		"MAILGUN_SMTP_LOGIN": "sender@mail.example",
	})
	deps.newMailer = func(config.Config) (pairing.Notifier, error) {
		events = append(events, "mailer")
		return notifierFunc(func(context.Context, string, []string, []string) error {
			events = append(events, "mail")
			return nil
		}), nil
	}
	if err := run(context.Background(), []string{"pair"}, deps); err != nil {
		t.Fatal(err)
	}
	want := []string{"mailer", "due", "load", "mail", "save"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestRunPairStopsAtFirstFailedRound(t *testing.T) {
	loadCalls := 0
	repository := &fakeApplicationStore{
		dueRoundsFunc: func(context.Context, time.Time) ([]pairing.ScheduledRound, error) {
			return []pairing.ScheduledRound{{Organization: "first", Number: 1}, {Organization: "second", Number: 2}}, nil
		},
		loadMembersFunc: func(context.Context, string) (pairing.MembersMap, error) {
			loadCalls++
			return nil, errors.New("load failed")
		},
	}
	deps := baseDependencies(repository)
	deps.getenv = mapEnvironment(map[string]string{
		"DATABASE_URL":       "postgres://database",
		"MAILGUN_DOMAIN":     "mail.example",
		"MAILGUN_API_KEY":    "key",
		"MAILGUN_SMTP_LOGIN": "sender@mail.example",
	})
	deps.newMailer = func(config.Config) (pairing.Notifier, error) {
		return notifierFunc(func(context.Context, string, []string, []string) error { return nil }), nil
	}
	err := run(context.Background(), []string{"pair"}, deps)
	if err == nil || !strings.Contains(err.Error(), "first") {
		t.Fatalf("run error = %v", err)
	}
	if loadCalls != 1 {
		t.Fatalf("load calls = %d, want fail-fast 1", loadCalls)
	}
}

func TestRunMigrateAppliesSchemaThenReconstructsHistory(t *testing.T) {
	events := make([]string, 0)
	repository := &fakeApplicationStore{
		reconstructFunc: func(context.Context) (store.HistoryReconstructionResult, error) {
			events = append(events, "reconstruct")
			return store.HistoryReconstructionResult{Completed: true}, nil
		},
	}
	deps := baseDependencies(repository)
	deps.getenv = mapEnvironment(map[string]string{"DATABASE_URL": "postgres://database"})
	deps.applyMigrations = func(context.Context, *sql.DB) error {
		events = append(events, "schema")
		return nil
	}
	deps.newStore = func(*sql.DB) (applicationStore, error) {
		events = append(events, "store")
		return repository, nil
	}
	var output bytes.Buffer
	deps.stdout = &output
	if err := run(context.Background(), []string{"migrate"}, deps); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"schema", "store", "reconstruct"}) {
		t.Fatalf("events = %#v", events)
	}
	if got, want := output.String(), "Done migrating to 'last round with' data structure in pairing algorithm!\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func baseDependencies(repository applicationStore) dependencies {
	return dependencies{
		getenv:          mapEnvironment(map[string]string{"DATABASE_URL": "postgres://database"}),
		stdout:          io.Discard,
		openDatabase:    func(context.Context, string) (*sql.DB, error) { return nil, nil },
		closeDatabase:   func(*sql.DB) error { return nil },
		applyMigrations: func(context.Context, *sql.DB) error { return nil },
		newStore:        func(*sql.DB) (applicationStore, error) { return repository, nil },
		buildWebHandler: func(config.Config, applicationStore, *slog.Logger, string) (http.Handler, error) {
			return http.NotFoundHandler(), nil
		},
		listenAndServe:  func(*http.Server) error { return errors.New("unexpected web server") },
		clock:           fixedClock{now: time.Date(2026, 11, 25, 12, 0, 0, 0, time.FixedZone("offset", 3600))},
		random:          pairing.RandomFunc(func(int) int { return 0 }),
		staticDirectory: staticDirectory,
	}
}

func mapEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func pairedStore(events *[]string) *fakeApplicationStore {
	return &fakeApplicationStore{
		dueRoundsFunc: func(_ context.Context, before time.Time) ([]pairing.ScheduledRound, error) {
			*events = append(*events, "due")
			if before.Location() != time.UTC {
				return nil, errors.New("scheduler clock was not converted to UTC")
			}
			return []pairing.ScheduledRound{{Organization: "org", Number: 3}}, nil
		},
		loadMembersFunc: func(context.Context, string) (pairing.MembersMap, error) {
			*events = append(*events, "load")
			return pairing.MembersMap{
				"a@example.com": {ID: "a@example.com", Name: "A", Trait: "x", LastRoundWith: map[string]int{"b@example.com": -1}},
				"b@example.com": {ID: "b@example.com", Name: "B", Trait: "x", LastRoundWith: map[string]int{"a@example.com": -1}},
			}, nil
		},
		saveRoundFunc: func(context.Context, string, pairing.Round, pairing.MembersMap) error {
			*events = append(*events, "save")
			return nil
		},
	}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type notifierFunc func(context.Context, string, []string, []string) error

func (f notifierFunc) Notify(ctx context.Context, organization string, emails, names []string) error {
	return f(ctx, organization, emails, names)
}

type fakeApplicationStore struct {
	dueRoundsFunc   func(context.Context, time.Time) ([]pairing.ScheduledRound, error)
	loadMembersFunc func(context.Context, string) (pairing.MembersMap, error)
	saveRoundFunc   func(context.Context, string, pairing.Round, pairing.MembersMap) error
	reconstructFunc func(context.Context) (store.HistoryReconstructionResult, error)
	httpCalls       int
}

func (s *fakeApplicationStore) OrganizationsByAdmin(context.Context, string) ([]string, error) {
	s.httpCalls++
	return nil, nil
}
func (s *fakeApplicationStore) CreateOrganization(context.Context, string, string) error {
	s.httpCalls++
	return nil
}
func (s *fakeApplicationStore) CrossMatchTrait(context.Context, string) (*string, error) {
	s.httpCalls++
	return nil, nil
}
func (s *fakeApplicationStore) SetCrossMatchTrait(context.Context, string, *string) error {
	s.httpCalls++
	return nil
}
func (s *fakeApplicationStore) Members(context.Context, string, bool) ([]store.Member, error) {
	s.httpCalls++
	return nil, nil
}
func (s *fakeApplicationStore) SynchronizeMembersCSV(context.Context, string, io.Reader) (store.MemberSyncPlan, error) {
	s.httpCalls++
	return store.MemberSyncPlan{}, nil
}
func (s *fakeApplicationStore) ListRounds(context.Context, string) ([]string, error) {
	s.httpCalls++
	return nil, nil
}
func (s *fakeApplicationStore) AddRound(context.Context, string, string) (int, error) {
	s.httpCalls++
	return 0, nil
}
func (s *fakeApplicationStore) RescheduleRound(context.Context, string, string, int) error {
	s.httpCalls++
	return nil
}
func (s *fakeApplicationStore) DeleteRound(context.Context, string, int) error {
	s.httpCalls++
	return nil
}
func (s *fakeApplicationStore) PairHistory(context.Context, string) ([][]store.HistoricalPair, error) {
	s.httpCalls++
	return nil, nil
}
func (s *fakeApplicationStore) DueRounds(ctx context.Context, before time.Time) ([]pairing.ScheduledRound, error) {
	if s.dueRoundsFunc == nil {
		return nil, nil
	}
	return s.dueRoundsFunc(ctx, before)
}
func (s *fakeApplicationStore) LoadMembers(ctx context.Context, organization string) (pairing.MembersMap, error) {
	if s.loadMembersFunc == nil {
		return pairing.MembersMap{}, nil
	}
	return s.loadMembersFunc(ctx, organization)
}
func (s *fakeApplicationStore) SaveRound(ctx context.Context, organization string, round pairing.Round, members pairing.MembersMap) error {
	if s.saveRoundFunc == nil {
		return nil
	}
	return s.saveRoundFunc(ctx, organization, round, members)
}
func (s *fakeApplicationStore) ReconstructPairHistory(ctx context.Context) (store.HistoryReconstructionResult, error) {
	if s.reconstructFunc == nil {
		return store.HistoryReconstructionResult{}, nil
	}
	return s.reconstructFunc(ctx)
}
