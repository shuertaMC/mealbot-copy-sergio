package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/johnamadeo/mealbot/internal/domain"
)

const (
	organizationCreatedText = "Successfully created new organization"
	traitUpdatedText        = "Successfully set the cross match trait"
)

// OrganizationRepository is the organization persistence surface consumed by
// OrganizationService. PostgreSQL satisfies it without an adapter.
type OrganizationRepository interface {
	ListOrganizations(context.Context, string) ([]domain.Organization, error)
	CreateOrganization(context.Context, domain.Organization) error
	UpdateCrossMatchTrait(context.Context, string, *string) error
}

// OrganizationUseCases is the narrow handler-facing organization contract.
type OrganizationUseCases interface {
	ListOrganizationNames(context.Context, string) ([]string, error)
	Create(context.Context, string, string) error
	SetCrossMatchTrait(context.Context, string, *string) error
}

// OrganizationService validates organization operations and presents stable,
// deterministic values to HTTP handlers.
type OrganizationService struct {
	repository OrganizationRepository
}

// NewOrganizationService constructs organization use cases over repository.
func NewOrganizationService(repository OrganizationRepository) *OrganizationService {
	return &OrganizationService{repository: repository}
}

// ListOrganizationNames lists an administrator's organization names in
// deterministic lexical order.
func (s *OrganizationService) ListOrganizationNames(ctx context.Context, admin string) ([]string, error) {
	admin = strings.TrimSpace(admin)
	if err := domain.RequireNonEmpty("organization.admin", admin); err != nil {
		return nil, err
	}
	organizations, err := s.repository.ListOrganizations(ctx, admin)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(organizations))
	for _, organization := range organizations {
		names = append(names, organization.Name)
	}
	sort.Strings(names)
	return names, nil
}

// Create validates and persists one organization.
func (s *OrganizationService) Create(ctx context.Context, name, admin string) error {
	organization, err := domain.NewOrganization(name, admin, nil)
	if err != nil {
		return err
	}
	return s.repository.CreateOrganization(ctx, organization)
}

// SetCrossMatchTrait sets a trait, or clears it when trait is nil or empty.
func (s *OrganizationService) SetCrossMatchTrait(ctx context.Context, organization string, trait *string) error {
	organization = strings.TrimSpace(organization)
	if err := domain.RequireNonEmpty("organization.name", organization); err != nil {
		return err
	}
	if trait != nil && *trait == "" {
		trait = nil
	}
	if trait != nil && strings.TrimSpace(*trait) == "" {
		return domain.NewValidationError("organization.crossMatchTrait", "must not be blank when set")
	}
	return s.repository.UpdateCrossMatchTrait(ctx, organization, trait)
}

type organizationsResponse struct {
	Organizations []string `json:"orgs"`
}

type createOrganizationRequest struct {
	Organization string `json:"org"`
}

type setCrossMatchTraitRequest struct {
	Trait json.RawMessage `json:"trait"`
}

// NewOrganizationsHandler constructs authenticated-route behavior for
// GET /orgs?admin=. Authentication remains the caller's middleware concern.
func NewOrganizationsHandler(service OrganizationUseCases, logger *slog.Logger) http.Handler {
	responses := newResponder(logger)
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			responses.methodNotAllowed(w, http.MethodGet)
			return
		}
		admin, err := QueryValue(request, "admin")
		if err != nil {
			responses.failure(request.Context(), w, "list organizations", err)
			return
		}
		names, err := service.ListOrganizationNames(request.Context(), admin)
		if err != nil {
			responses.failure(request.Context(), w, "list organizations", err)
			return
		}
		responses.json(w, http.StatusOK, organizationsResponse{Organizations: names})
	})
}

// NewCreateOrganizationHandler constructs authenticated-route behavior for
// POST /org?admin= with bounded, strict JSON decoding.
func NewCreateOrganizationHandler(service OrganizationUseCases, maxBodyBytes int64, logger *slog.Logger) http.Handler {
	responses := newResponder(logger)
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			responses.methodNotAllowed(w, http.MethodPost)
			return
		}
		admin, err := QueryValue(request, "admin")
		if err != nil {
			responses.failure(request.Context(), w, "create organization", err)
			return
		}
		var body createOrganizationRequest
		if err := decodeJSONBody(request, maxBodyBytes, &body); err != nil {
			responses.failure(request.Context(), w, "create organization", err)
			return
		}
		if err := service.Create(request.Context(), body.Organization, admin); err != nil {
			responses.failure(request.Context(), w, "create organization", err)
			return
		}
		responses.text(w, http.StatusCreated, organizationCreatedText)
	})
}

// NewCrossMatchTraitHandler constructs authenticated-route behavior for
// POST /crossmatchtrait?org=. JSON null or an empty string clears the stored
// trait; a non-empty string sets it, while a missing or differently typed trait
// is invalid.
func NewCrossMatchTraitHandler(service OrganizationUseCases, maxBodyBytes int64, logger *slog.Logger) http.Handler {
	responses := newResponder(logger)
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			responses.methodNotAllowed(w, http.MethodPost)
			return
		}
		organization, err := QueryValue(request, "org")
		if err != nil {
			responses.failure(request.Context(), w, "update cross-match trait", err)
			return
		}
		var body setCrossMatchTraitRequest
		if err := decodeJSONBody(request, maxBodyBytes, &body); err != nil {
			responses.failure(request.Context(), w, "update cross-match trait", err)
			return
		}
		trait, err := decodeNullableTrait(body.Trait)
		if err != nil {
			responses.failure(request.Context(), w, "update cross-match trait", err)
			return
		}
		if err := service.SetCrossMatchTrait(request.Context(), organization, trait); err != nil {
			responses.failure(request.Context(), w, "update cross-match trait", err)
			return
		}
		responses.text(w, http.StatusCreated, traitUpdatedText)
	})
}

func decodeNullableTrait(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 {
		return nil, domain.NewValidationError("trait", "is required")
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var trait string
	if err := json.Unmarshal(raw, &trait); err != nil {
		return nil, domain.NewValidationError("trait", "must be a string or null")
	}
	if trait == "" {
		return nil, nil
	}
	return &trait, nil
}
