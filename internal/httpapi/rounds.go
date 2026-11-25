package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johnamadeo/mealbot/internal/domain"
)

const (
	roundTimestampLayout = "2006-01-02 15:04:05Z"
	roundAddedText       = "Successfully scheduled a new round"
	roundRemovedText     = "Successfully removed round"
	roundRescheduledText = "Succesfully changed date of the round"
)

// RoundRepository is the round persistence surface consumed by RoundService.
type RoundRepository interface {
	ListRounds(context.Context, string) ([]domain.Round, error)
	AddRound(context.Context, string, time.Time) (domain.Round, error)
	RescheduleRound(context.Context, string, int, time.Time) error
	DeleteRound(context.Context, string, int) error
}

// RoundUseCases is the narrow handler-facing scheduling contract.
type RoundUseCases interface {
	List(context.Context, string) ([]string, error)
	Add(context.Context, string, string) error
	Reschedule(context.Context, string, int, string) error
	Remove(context.Context, string, int) error
}

// RoundService validates and normalizes legacy round requests.
type RoundService struct{ repository RoundRepository }

// NewRoundService constructs round scheduling use cases over repository.
func NewRoundService(repository RoundRepository) *RoundService {
	return &RoundService{repository: repository}
}

// List returns ordered timestamps in the legacy UTC second-resolution shape.
func (s *RoundService) List(ctx context.Context, organization string) ([]string, error) {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return nil, domain.NewValidationError("round.organization", "must not be empty")
	}
	rounds, err := s.repository.ListRounds(ctx, organization)
	if err != nil {
		return nil, err
	}
	result := make([]string, len(rounds))
	for index, round := range rounds {
		result[index] = round.ScheduledAt.UTC().Truncate(time.Second).Format(roundTimestampLayout)
	}
	return result, nil
}

// Add parses a supported timestamp and schedules a new round.
func (s *RoundService) Add(ctx context.Context, organization, timestamp string) error {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return domain.NewValidationError("round.organization", "must not be empty")
	}
	parsed, err := ParseRoundTimestamp(timestamp)
	if err != nil {
		return err
	}
	_, err = s.repository.AddRound(ctx, organization, parsed)
	return err
}

// Reschedule parses a supported timestamp and updates a non-negative round ID.
func (s *RoundService) Reschedule(ctx context.Context, organization string, roundID int, timestamp string) error {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return domain.NewValidationError("round.organization", "must not be empty")
	}
	if roundID < 0 {
		return domain.NewValidationError("round.id", "must be non-negative")
	}
	parsed, err := ParseRoundTimestamp(timestamp)
	if err != nil {
		return err
	}
	return s.repository.RescheduleRound(ctx, organization, roundID, parsed)
}

// Remove deletes and compacts a non-negative round ID.
func (s *RoundService) Remove(ctx context.Context, organization string, roundID int) error {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return domain.NewValidationError("round.organization", "must not be empty")
	}
	if roundID < 0 {
		return domain.NewValidationError("round.id", "must be non-negative")
	}
	return s.repository.DeleteRound(ctx, organization, roundID)
}

// ParseRoundTimestamp accepts RFC3339 timestamps and the declared legacy
// PostgreSQL-compatible second-resolution layouts, returning normalized UTC.
func ParseRoundTimestamp(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	layouts := []string{time.RFC3339Nano, "2006-01-02 15:04:05Z07:00", roundTimestampLayout, "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC().Truncate(time.Second), nil
		}
	}
	return time.Time{}, domain.NewValidationError("round", "must be an RFC3339 or legacy timestamp")
}

type roundsResponse struct {
	Rounds []string `json:"rounds"`
}

// NewRoundsHandler constructs authenticated GET /rounds?org= behavior.
func NewRoundsHandler(service RoundUseCases, logger *slog.Logger) http.Handler {
	responses := newResponder(logger)
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			responses.methodNotAllowed(w, http.MethodGet)
			return
		}
		organization, err := QueryValue(request, "org")
		if err != nil {
			responses.failure(request.Context(), w, "list rounds", err)
			return
		}
		rounds, err := service.List(request.Context(), organization)
		if err != nil {
			responses.failure(request.Context(), w, "list rounds", err)
			return
		}
		responses.json(w, http.StatusOK, roundsResponse{Rounds: rounds})
	})
}

// NewRoundHandler constructs authenticated POST/DELETE /round behavior. A
// POST with roundId reschedules; a POST without that key adds a round.
func NewRoundHandler(service RoundUseCases, logger *slog.Logger) http.Handler {
	responses := newResponder(logger)
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPost:
			values, err := QueryValues(request, "org", "round")
			if err != nil {
				responses.failure(request.Context(), w, "schedule round", err)
				return
			}
			if _, supplied := request.URL.Query()["roundId"]; supplied {
				roundID, err := queryRoundID(request)
				if err != nil {
					responses.failure(request.Context(), w, "reschedule round", err)
					return
				}
				if err := service.Reschedule(request.Context(), values[0], roundID, values[1]); err != nil {
					responses.failure(request.Context(), w, "reschedule round", err)
					return
				}
				responses.text(w, http.StatusCreated, roundRescheduledText)
				return
			}
			if err := service.Add(request.Context(), values[0], values[1]); err != nil {
				responses.failure(request.Context(), w, "schedule round", err)
				return
			}
			responses.text(w, http.StatusCreated, roundAddedText)
		case http.MethodDelete:
			organization, err := QueryValue(request, "org")
			if err != nil {
				responses.failure(request.Context(), w, "remove round", err)
				return
			}
			roundID, err := queryRoundID(request)
			if err != nil {
				responses.failure(request.Context(), w, "remove round", err)
				return
			}
			if err := service.Remove(request.Context(), organization, roundID); err != nil {
				responses.failure(request.Context(), w, "remove round", err)
				return
			}
			responses.text(w, http.StatusCreated, roundRemovedText)
		default:
			responses.methodNotAllowed(w, http.MethodPost, http.MethodDelete)
		}
	})
}

func queryRoundID(request *http.Request) (int, error) {
	value, err := QueryValue(request, "roundId")
	if err != nil {
		return 0, err
	}
	roundID, err := strconv.Atoi(value)
	if err != nil || roundID < 0 {
		return 0, domain.NewValidationError("round.id", fmt.Sprintf("%q must be a non-negative integer", value))
	}
	return roundID, nil
}
