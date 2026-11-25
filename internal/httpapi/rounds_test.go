package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnamadeo/mealbot/internal/domain"
)

type fakeRoundRepository struct {
	rounds      []domain.Round
	added       time.Time
	rescheduled time.Time
	removed     int
	err         error
}

func (f *fakeRoundRepository) ListRounds(context.Context, string) ([]domain.Round, error) {
	return f.rounds, f.err
}
func (f *fakeRoundRepository) AddRound(_ context.Context, organization string, at time.Time) (domain.Round, error) {
	f.added = at
	return domain.Round{Organization: organization, ScheduledAt: at}, f.err
}
func (f *fakeRoundRepository) RescheduleRound(_ context.Context, _ string, id int, at time.Time) error {
	f.removed, f.rescheduled = id, at
	return f.err
}
func (f *fakeRoundRepository) DeleteRound(_ context.Context, _ string, id int) error {
	f.removed = id
	return f.err
}

func TestParseRoundTimestampAndServiceNormalization(t *testing.T) {
	for _, input := range []string{"2026-10-02T03:04:05.987-04:00", "2026-10-02 07:04:05Z", "2026-10-02 07:04:05"} {
		parsed, err := ParseRoundTimestamp(input)
		if err != nil || parsed.Location() != time.UTC || parsed.Nanosecond() != 0 {
			t.Fatalf("ParseRoundTimestamp(%q) = %v, %v", input, parsed, err)
		}
	}
	if _, err := ParseRoundTimestamp("Friday"); !domain.IsValidation(err) {
		t.Fatalf("malformed timestamp error = %v", err)
	}

	repository := &fakeRoundRepository{rounds: []domain.Round{{ScheduledAt: time.Date(2026, 1, 2, 3, 4, 5, 999, time.FixedZone("x", 3600))}}}
	service := NewRoundService(repository)
	rounds, err := service.List(context.Background(), " org ")
	if err != nil || len(rounds) != 1 || rounds[0] != "2026-01-02 02:04:05Z" {
		t.Fatalf("List() = %#v, %v", rounds, err)
	}
	if err := service.Add(context.Background(), "org", "2026-01-02T03:04:05.9Z"); err != nil || repository.added.Nanosecond() != 0 {
		t.Fatalf("Add() = %v, stored %v", err, repository.added)
	}
	if err := service.Reschedule(context.Background(), "org", -1, "2026-01-02T03:04:05Z"); !domain.IsValidation(err) {
		t.Fatalf("negative Reschedule() = %v", err)
	}
	if err := service.Remove(context.Background(), "org", -1); !domain.IsValidation(err) {
		t.Fatalf("negative Remove() = %v", err)
	}
}

type fakeRoundUseCases struct {
	operation    string
	organization string
	timestamp    string
	id           int
	err          error
}

func (f *fakeRoundUseCases) List(_ context.Context, organization string) ([]string, error) {
	f.operation, f.organization = "list", organization
	return []string{"2026-01-02 03:04:05Z"}, f.err
}
func (f *fakeRoundUseCases) Add(_ context.Context, organization, timestamp string) error {
	f.operation, f.organization, f.timestamp = "add", organization, timestamp
	return f.err
}
func (f *fakeRoundUseCases) Reschedule(_ context.Context, organization string, id int, timestamp string) error {
	f.operation, f.organization, f.id, f.timestamp = "reschedule", organization, id, timestamp
	return f.err
}
func (f *fakeRoundUseCases) Remove(_ context.Context, organization string, id int) error {
	f.operation, f.organization, f.id = "remove", organization, id
	return f.err
}

func TestRoundHandlersContracts(t *testing.T) {
	tests := []struct {
		method, target, operation, body, contentType string
		status                                       int
	}{
		{http.MethodGet, "/rounds?org=o", "list", `{"rounds":["2026-01-02 03:04:05Z"]}` + "\n", "application/json; charset=utf-8", http.StatusOK},
		{http.MethodPost, "/round?org=o&round=2026-01-02T03:04:05Z", "add", roundAddedText, "text/plain; charset=utf-8", http.StatusCreated},
		{http.MethodPost, "/round?org=o&round=2026-01-02T03:04:05Z&roundId=2", "reschedule", roundRescheduledText, "text/plain; charset=utf-8", http.StatusCreated},
		{http.MethodDelete, "/round?org=o&roundId=2", "remove", roundRemovedText, "text/plain; charset=utf-8", http.StatusCreated},
	}
	for _, test := range tests {
		useCases := &fakeRoundUseCases{}
		handler := NewRoundHandler(useCases, nil)
		if strings.HasPrefix(test.target, "/rounds") {
			handler = NewRoundsHandler(useCases, nil)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
		if recorder.Code != test.status || recorder.Body.String() != test.body || recorder.Header().Get("Content-Type") != test.contentType || useCases.operation != test.operation {
			t.Errorf("%s %s = %d %q type %q operation %q", test.method, test.target, recorder.Code, recorder.Body.String(), recorder.Header().Get("Content-Type"), useCases.operation)
		}
	}
}

func TestRoundsHandlerQueryCardinalityAndMethod(t *testing.T) {
	tests := []struct {
		name, method, target string
		status               int
		body                 string
	}{
		{name: "missing org", method: http.MethodGet, target: "/rounds", status: http.StatusBadRequest, body: "{\"error\":\"invalid request\"}\n"},
		{name: "repeated org", method: http.MethodGet, target: "/rounds?org=a&org=b", status: http.StatusBadRequest, body: "{\"error\":\"invalid request\"}\n"},
		{name: "empty org", method: http.MethodGet, target: "/rounds?org=", status: http.StatusBadRequest, body: "{\"error\":\"invalid request\"}\n"},
		{name: "unsupported method", method: http.MethodPost, target: "/rounds?org=o", status: http.StatusMethodNotAllowed, body: "{\"error\":\"method not allowed\"}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useCases := &fakeRoundUseCases{}
			recorder := httptest.NewRecorder()
			NewRoundsHandler(useCases, nil).ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
			assertHTTPResponse(t, recorder, test.status, test.body, "application/json; charset=utf-8")
			if useCases.operation != "" {
				t.Fatalf("invalid request invoked %q", useCases.operation)
			}
		})
	}
}

func TestRoundHandlerAddQueryCardinality(t *testing.T) {
	const timestamp = "2026-01-02T03:04:05Z"
	tests := []struct {
		name, target string
	}{
		{name: "missing org", target: "/round?round=" + timestamp},
		{name: "repeated org", target: "/round?org=a&org=b&round=" + timestamp},
		{name: "empty org", target: "/round?org=&round=" + timestamp},
		{name: "missing round", target: "/round?org=o"},
		{name: "repeated round", target: "/round?org=o&round=" + timestamp + "&round=" + timestamp},
		{name: "empty round", target: "/round?org=o&round="},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRoundQueryFailure(t, http.MethodPost, test.target)
		})
	}
}

func TestRoundHandlerRescheduleQueryCardinality(t *testing.T) {
	const timestamp = "2026-01-02T03:04:05Z"
	tests := []struct {
		name, target string
	}{
		{name: "missing org", target: "/round?round=" + timestamp + "&roundId=2"},
		{name: "repeated org", target: "/round?org=a&org=b&round=" + timestamp + "&roundId=2"},
		{name: "empty org", target: "/round?org=&round=" + timestamp + "&roundId=2"},
		{name: "missing round", target: "/round?org=o&roundId=2"},
		{name: "repeated round", target: "/round?org=o&round=" + timestamp + "&round=" + timestamp + "&roundId=2"},
		{name: "empty round", target: "/round?org=o&round=&roundId=2"},
		{name: "repeated roundId", target: "/round?org=o&round=" + timestamp + "&roundId=1&roundId=2"},
		{name: "empty roundId", target: "/round?org=o&round=" + timestamp + "&roundId="},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRoundQueryFailure(t, http.MethodPost, test.target)
		})
	}
}

func TestRoundHandlerDeleteQueryCardinality(t *testing.T) {
	tests := []struct {
		name, target string
	}{
		{name: "missing org", target: "/round?roundId=2"},
		{name: "repeated org", target: "/round?org=a&org=b&roundId=2"},
		{name: "empty org", target: "/round?org=&roundId=2"},
		{name: "missing roundId", target: "/round?org=o"},
		{name: "repeated roundId", target: "/round?org=o&roundId=1&roundId=2"},
		{name: "empty roundId", target: "/round?org=o&roundId="},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRoundQueryFailure(t, http.MethodDelete, test.target)
		})
	}
}

func assertRoundQueryFailure(t *testing.T, method, target string) {
	t.Helper()
	useCases := &fakeRoundUseCases{}
	recorder := httptest.NewRecorder()
	NewRoundHandler(useCases, nil).ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	assertHTTPResponse(t, recorder, http.StatusBadRequest, "{\"error\":\"invalid request\"}\n", "application/json; charset=utf-8")
	if useCases.operation != "" {
		t.Fatalf("invalid query invoked %q", useCases.operation)
	}
}

func TestRoundHandlersValidationAndErrorMapping(t *testing.T) {
	tests := []struct {
		method, target, body string
		err                  error
		status               int
	}{
		{http.MethodPost, "/round?org=o&round=x&roundId=-1", "{\"error\":\"invalid request\"}\n", nil, http.StatusBadRequest},
		{http.MethodDelete, "/round?org=o&roundId=x", "{\"error\":\"invalid request\"}\n", nil, http.StatusBadRequest},
		{http.MethodDelete, "/round?org=o&roundId=1", "{\"error\":\"not found\"}\n", domain.NewNotFoundError("round", "1"), http.StatusNotFound},
		{http.MethodPost, "/round?org=o&round=x", "{\"error\":\"conflict\"}\n", domain.NewConflictError("round", "busy", nil), http.StatusConflict},
		{http.MethodGet, "/rounds?org=o", "{\"error\":\"internal server error\"}\n", errors.New("secret database detail"), http.StatusInternalServerError},
		{http.MethodPut, "/round?org=o", "{\"error\":\"method not allowed\"}\n", nil, http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		useCases := &fakeRoundUseCases{err: test.err}
		handler := NewRoundHandler(useCases, nil)
		if strings.HasPrefix(test.target, "/rounds") {
			handler = NewRoundsHandler(useCases, nil)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
		if recorder.Code != test.status || recorder.Body.String() != test.body || recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" || strings.Contains(recorder.Body.String(), "secret") {
			t.Errorf("%s %s = %d %q", test.method, test.target, recorder.Code, recorder.Body.String())
		}
	}
}
