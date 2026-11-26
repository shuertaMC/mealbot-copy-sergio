package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/johnamadeo/mealbot/internal/domain"
)

type fakeOrganizationRepository struct {
	organizations []domain.Organization
	listErr       error
	createErr     error
	updateErr     error
	created       domain.Organization
	updatedOrg    string
	updatedTrait  *string
}

func (f *fakeOrganizationRepository) ListOrganizations(context.Context, string) ([]domain.Organization, error) {
	return f.organizations, f.listErr
}

func (f *fakeOrganizationRepository) CreateOrganization(_ context.Context, organization domain.Organization) error {
	f.created = organization
	return f.createErr
}

func (f *fakeOrganizationRepository) UpdateCrossMatchTrait(_ context.Context, organization string, trait *string) error {
	f.updatedOrg = organization
	f.updatedTrait = trait
	return f.updateErr
}

func TestOrganizationServiceValidationOrderingAndClearing(t *testing.T) {
	t.Parallel()
	repository := &fakeOrganizationRepository{organizations: []domain.Organization{{Name: "z"}, {Name: "a"}}}
	service := NewOrganizationService(repository)
	names, err := service.ListOrganizationNames(context.Background(), " admin ")
	if err != nil || !reflect.DeepEqual(names, []string{"a", "z"}) {
		t.Fatalf("names = %#v, error = %v", names, err)
	}
	if err := service.Create(context.Background(), " org ", " admin "); err != nil {
		t.Fatal(err)
	}
	if repository.created.Name != "org" || repository.created.Admin != "admin" {
		t.Fatalf("created = %#v", repository.created)
	}
	trait := " college "
	if err := service.SetCrossMatchTrait(context.Background(), " org ", &trait); err != nil {
		t.Fatal(err)
	}
	if repository.updatedOrg != "org" || repository.updatedTrait == nil || *repository.updatedTrait != " college " {
		t.Fatalf("update = %q, %#v", repository.updatedOrg, repository.updatedTrait)
	}
	if err := service.SetCrossMatchTrait(context.Background(), "org", nil); err != nil {
		t.Fatal(err)
	}
	if repository.updatedTrait != nil {
		t.Fatalf("clear trait = %#v; want nil", repository.updatedTrait)
	}
	if err := service.Create(context.Background(), " ", "admin"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("create error = %v", err)
	}
	repository.updatedTrait = &trait
	empty := ""
	if err := service.SetCrossMatchTrait(context.Background(), "org", &empty); err != nil {
		t.Fatal(err)
	}
	if repository.updatedTrait != nil {
		t.Fatalf("empty trait = %#v; want cleared", repository.updatedTrait)
	}
	blank := "  "
	if err := service.SetCrossMatchTrait(context.Background(), "org", &blank); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("blank trait error = %v", err)
	}
}

type fakeOrganizationUseCases struct {
	names       []string
	err         error
	createdName string
	admin       string
	updatedOrg  string
	trait       *string
}

func (f *fakeOrganizationUseCases) ListOrganizationNames(context.Context, string) ([]string, error) {
	return f.names, f.err
}

func (f *fakeOrganizationUseCases) Create(_ context.Context, name, admin string) error {
	f.createdName, f.admin = name, admin
	return f.err
}

func (f *fakeOrganizationUseCases) SetCrossMatchTrait(_ context.Context, organization string, trait *string) error {
	f.updatedOrg, f.trait = organization, trait
	return f.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOrganizationsHandlerContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method string
		url    string
		err    error
		status int
		body   string
	}{
		{name: "success", method: http.MethodGet, url: "/orgs?admin=a", status: 200, body: "{\"orgs\":[\"alpha\"]}\n"},
		{name: "method", method: http.MethodPost, url: "/orgs?admin=a", status: 405, body: "{\"error\":\"method not allowed\"}\n"},
		{name: "missing", method: http.MethodGet, url: "/orgs", status: 400, body: "{\"error\":\"invalid request\"}\n"},
		{name: "repeated", method: http.MethodGet, url: "/orgs?admin=a&admin=b", status: 400, body: "{\"error\":\"invalid request\"}\n"},
		{name: "empty", method: http.MethodGet, url: "/orgs?admin=", status: 400, body: "{\"error\":\"invalid request\"}\n"},
		{name: "internal", method: http.MethodGet, url: "/orgs?admin=a", err: errors.New("sql secret"), status: 500, body: "{\"error\":\"internal server error\"}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeOrganizationUseCases{names: []string{"alpha"}, err: test.err}
			response := httptest.NewRecorder()
			NewOrganizationsHandler(service, testLogger()).ServeHTTP(response, httptest.NewRequest(test.method, test.url, nil))
			assertHTTPResponse(t, response, test.status, test.body, "application/json; charset=utf-8")
			if strings.Contains(response.Body.String(), "sql secret") {
				t.Fatal("internal error leaked")
			}
		})
	}
}

func TestCreateOrganizationHandlerContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method string
		url    string
		body   string
		err    error
		limit  int64
		status int
		want   string
		allow  string
	}{
		{name: "success", method: http.MethodPost, url: "/org?admin=owner", body: `{"org":"alpha"}`, status: 201, want: organizationCreatedText},
		{name: "method", method: http.MethodGet, url: "/org?admin=owner", status: 405, want: "{\"error\":\"method not allowed\"}\n", allow: http.MethodPost},
		{name: "missing admin", method: http.MethodPost, url: "/org", body: `{"org":"alpha"}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "repeated admin", method: http.MethodPost, url: "/org?admin=a&admin=b", body: `{"org":"alpha"}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "empty admin", method: http.MethodPost, url: "/org?admin=", body: `{"org":"alpha"}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "malformed", method: http.MethodPost, url: "/org?admin=owner", body: `{`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "unknown field", method: http.MethodPost, url: "/org?admin=owner", body: `{"org":"a","x":1}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "trailing", method: http.MethodPost, url: "/org?admin=owner", body: `{"org":"a"}{}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "oversized", method: http.MethodPost, url: "/org?admin=owner", body: `{"org":"alpha"}`, limit: 2, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "conflict", method: http.MethodPost, url: "/org?admin=owner", body: `{"org":"alpha"}`, err: domain.NewConflictError("org", "duplicate", nil), status: 409, want: "{\"error\":\"conflict\"}\n"},
		{name: "internal", method: http.MethodPost, url: "/org?admin=owner", body: `{"org":"alpha"}`, err: errors.New("postgres password"), status: 500, want: "{\"error\":\"internal server error\"}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeOrganizationUseCases{err: test.err}
			response := httptest.NewRecorder()
			NewCreateOrganizationHandler(service, test.limit, testLogger()).ServeHTTP(response, httptest.NewRequest(test.method, test.url, strings.NewReader(test.body)))
			contentType := "application/json; charset=utf-8"
			if test.status == 201 {
				contentType = "text/plain; charset=utf-8"
			}
			assertHTTPResponse(t, response, test.status, test.want, contentType)
			if got := response.Header().Get("Allow"); got != test.allow {
				t.Fatalf("Allow = %q, want %q", got, test.allow)
			}
			if test.status == 201 && (service.createdName != "alpha" || service.admin != "owner") {
				t.Fatalf("create input = %q, %q", service.createdName, service.admin)
			}
		})
	}
}

func TestCrossMatchTraitHandlerSetClearAndErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		method string
		url    string
		body   string
		err    error
		status int
		set    *string
		want   string
	}{
		{name: "set", method: http.MethodPost, url: "/crossmatchtrait?org=alpha", body: `{"trait":" college "}`, status: 201, set: stringPointer(" college "), want: traitUpdatedText},
		{name: "clear", method: http.MethodPost, url: "/crossmatchtrait?org=alpha", body: `{"trait":null}`, status: 201, want: traitUpdatedText},
		{name: "not found", method: http.MethodPost, url: "/crossmatchtrait?org=alpha", body: `{"trait":"college"}`, err: domain.NewNotFoundError("org", "alpha"), status: 404, want: "{\"error\":\"not found\"}\n"},
		{name: "method", method: http.MethodGet, url: "/crossmatchtrait?org=alpha", status: 405, want: "{\"error\":\"method not allowed\"}\n"},
		{name: "missing query", method: http.MethodPost, url: "/crossmatchtrait", body: `{"trait":"college"}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "repeated query", method: http.MethodPost, url: "/crossmatchtrait?org=a&org=b", body: `{"trait":"college"}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "empty query", method: http.MethodPost, url: "/crossmatchtrait?org=", body: `{"trait":"college"}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "missing trait", method: http.MethodPost, url: "/crossmatchtrait?org=alpha", body: `{}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "empty trait clears", method: http.MethodPost, url: "/crossmatchtrait?org=alpha", body: `{"trait":""}`, status: 201, want: traitUpdatedText},
		{name: "wrong trait type", method: http.MethodPost, url: "/crossmatchtrait?org=alpha", body: `{"trait":4}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "unknown field", method: http.MethodPost, url: "/crossmatchtrait?org=alpha", body: `{"trait":"x","other":1}`, status: 400, want: "{\"error\":\"invalid request\"}\n"},
		{name: "internal", method: http.MethodPost, url: "/crossmatchtrait?org=alpha", body: `{"trait":"x"}`, err: errors.New("sql details"), status: 500, want: "{\"error\":\"internal server error\"}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeOrganizationUseCases{err: test.err}
			response := httptest.NewRecorder()
			NewCrossMatchTraitHandler(service, 1024, testLogger()).ServeHTTP(response, httptest.NewRequest(test.method, test.url, bytes.NewBufferString(test.body)))
			contentType := "application/json; charset=utf-8"
			if test.status == 201 {
				contentType = "text/plain; charset=utf-8"
			}
			assertHTTPResponse(t, response, test.status, test.want, contentType)
			if test.status == 201 {
				if service.updatedOrg != "alpha" || !equalStringPointers(service.trait, test.set) {
					t.Fatalf("body=%q org=%q trait=%v", response.Body.String(), service.updatedOrg, service.trait)
				}
			}
			if strings.Contains(response.Body.String(), "sql details") {
				t.Fatal("internal detail leaked")
			}
		})
	}
}

func assertHTTPResponse(t *testing.T, response *httptest.ResponseRecorder, status int, body, contentType string) {
	t.Helper()
	if response.Code != status || response.Body.String() != body || response.Header().Get("Content-Type") != contentType {
		t.Fatalf("response = status %d, type %q, body %q; want %d, %q, %q", response.Code, response.Header().Get("Content-Type"), response.Body.String(), status, contentType, body)
	}
}

func stringPointer(value string) *string { return &value }

func equalStringPointers(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
