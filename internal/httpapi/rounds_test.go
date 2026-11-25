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
	operation string
	id        int
	err       error
}

func (f *fakeRoundUseCases) List(context.Context, string) ([]string, error) {
	f.operation = "list"
	return []string{"2026-01-02 03:04:05Z"}, f.err
}
func (f *fakeRoundUseCases) Add(context.Context, string, string) error {
	f.operation = "add"
	return f.err
}
func (f *fakeRoundUseCases) Reschedule(_ context.Context, _ string, id int, _ string) error {
	f.operation, f.id = "reschedule", id
	return f.err
}
func (f *fakeRoundUseCases) Remove(_ context.Context, _ string, id int) error {
	f.operation, f.id = "remove", id
	return f.err
}

func TestRoundHandlersContracts(t *testing.T) {
	tests := []struct {
		method, target, operation, body string
		status                          int
	}{
		{http.MethodGet, "/rounds?org=o", "list", `{"rounds":["2026-01-02 03:04:05Z"]}` + "\n", http.StatusOK},
		{http.MethodPost, "/round?org=o&round=2026-01-02T03:04:05Z", "add", roundAddedText, http.StatusCreated},
		{http.MethodPost, "/round?org=o&round=2026-01-02T03:04:05Z&roundId=2", "reschedule", roundRescheduledText, http.StatusCreated},
		{http.MethodDelete, "/round?org=o&roundId=2", "remove", roundRemovedText, http.StatusCreated},
	}
	for _, test := range tests {
		useCases := &fakeRoundUseCases{}
		handler := NewRoundHandler(useCases, nil)
		if strings.HasPrefix(test.target, "/rounds") {
			handler = NewRoundsHandler(useCases, nil)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
		if recorder.Code != test.status || recorder.Body.String() != test.body || useCases.operation != test.operation {
			t.Errorf("%s %s = %d %q operation %q", test.method, test.target, recorder.Code, recorder.Body.String(), useCases.operation)
		}
	}
}

func TestRoundHandlersValidationAndErrorMapping(t *testing.T) {
	tests := []struct {
		method, target string
		err            error
		status         int
	}{
		{http.MethodGet, "/rounds", nil, http.StatusBadRequest},
		{http.MethodPost, "/round?org=o&round=x&roundId=-1", nil, http.StatusBadRequest},
		{http.MethodPost, "/round?org=o&round=x&roundId=1&roundId=2", nil, http.StatusBadRequest},
		{http.MethodDelete, "/round?org=o&roundId=x", nil, http.StatusBadRequest},
		{http.MethodDelete, "/round?org=o&roundId=1", domain.NewNotFoundError("round", "1"), http.StatusNotFound},
		{http.MethodPost, "/round?org=o&round=x", domain.NewConflictError("round", "busy", nil), http.StatusConflict},
		{http.MethodGet, "/rounds?org=o", errors.New("secret database detail"), http.StatusInternalServerError},
		{http.MethodPut, "/round?org=o", nil, http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		useCases := &fakeRoundUseCases{err: test.err}
		handler := NewRoundHandler(useCases, nil)
		if strings.HasPrefix(test.target, "/rounds") {
			handler = NewRoundsHandler(useCases, nil)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.target, nil))
		if recorder.Code != test.status || strings.Contains(recorder.Body.String(), "secret") {
			t.Errorf("%s %s = %d %q", test.method, test.target, recorder.Code, recorder.Body.String())
		}
	}
}
