package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeOrganizationStore struct {
	organizations []string
	trait         *string
	err           error
	createName    string
	createAdmin   string
	setOrg        string
	setTrait      *string
	calls         int
}

func (s *fakeOrganizationStore) OrganizationsByAdmin(_ context.Context, _ string) ([]string, error) {
	s.calls++
	return s.organizations, s.err
}

func (s *fakeOrganizationStore) CreateOrganization(_ context.Context, name, admin string) error {
	s.calls++
	s.createName, s.createAdmin = name, admin
	return s.err
}

func (s *fakeOrganizationStore) CrossMatchTrait(_ context.Context, _ string) (*string, error) {
	s.calls++
	return s.trait, s.err
}

func (s *fakeOrganizationStore) SetCrossMatchTrait(_ context.Context, organization string, trait *string) error {
	s.calls++
	s.setOrg, s.setTrait = organization, trait
	return s.err
}

func assertHandlerResponse(t *testing.T, handler http.HandlerFunc, method, target, body string, status int, response string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	handler(recorder, request)
	if recorder.Code != status || recorder.Body.String() != response {
		t.Fatalf("response = (%d, %q), want (%d, %q)", recorder.Code, recorder.Body.String(), status, response)
	}
}

func TestOrganizationHandlersPreserveSuccessContracts(t *testing.T) {
	trait := "cohort"
	store := &fakeOrganizationStore{organizations: []string{"alpha", "beta"}}
	handlers := NewOrganizationHandlers(store, NewResponder(nil))

	assertHandlerResponse(t, handlers.Organizations, http.MethodGet, "/orgs?admin=owner", "", http.StatusOK, `{"orgs":["alpha","beta"]}`)
	assertHandlerResponse(t, handlers.Create, http.MethodPost, "/org?admin=owner", `{"org":"alpha"}`, http.StatusOK, `{"Message":"Successfully created new organization"}`)
	if store.createName != "alpha" || store.createAdmin != "owner" {
		t.Fatalf("create arguments = (%q, %q)", store.createName, store.createAdmin)
	}
	assertHandlerResponse(t, handlers.Create, http.MethodPost, "/org?admin=%20%09", `{"org":" "}`, http.StatusOK, `{"Message":"Successfully created new organization"}`)
	if store.createName != " " || store.createAdmin != " \t" {
		t.Fatalf("whitespace create arguments = (%q, %q)", store.createName, store.createAdmin)
	}
	assertHandlerResponse(t, handlers.CrossMatchTrait, http.MethodPost, "/crossmatchtrait?org=alpha", `{"trait":"cohort"}`, http.StatusOK, `{"Message":"Successfully set the cross match trait"}`)
	if store.setOrg != "alpha" || store.setTrait == nil || *store.setTrait != trait {
		t.Fatalf("set trait arguments = (%q, %v)", store.setOrg, store.setTrait)
	}
}

func TestOrganizationsEndpointMethodRestriction(t *testing.T) {
	store := &fakeOrganizationStore{}
	handlers := NewOrganizationHandlers(store, NewResponder(nil))
	assertHandlerResponse(t, handlers.Organizations, http.MethodPost, "/orgs?admin=owner", "", http.StatusMethodNotAllowed, `{"Message":"Only GET requests are allowed at this route"}`)
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestOrganizationEndpointMethodRestriction(t *testing.T) {
	store := &fakeOrganizationStore{}
	handlers := NewOrganizationHandlers(store, NewResponder(nil))
	assertHandlerResponse(t, handlers.Create, http.MethodGet, "/org?admin=owner", "", http.StatusMethodNotAllowed, `{"Message":"Only POST requests are allowed at this route"}`)
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestCrossMatchTraitEndpointMethodRestriction(t *testing.T) {
	store := &fakeOrganizationStore{}
	handlers := NewOrganizationHandlers(store, NewResponder(nil))
	assertHandlerResponse(t, handlers.CrossMatchTrait, http.MethodGet, "/crossmatchtrait?org=alpha", "", http.StatusMethodNotAllowed, `{"Message":"Only POST requests are allowed at this route"}`)
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestOrganizationsEndpointRepositoryFailure(t *testing.T) {
	handlers := NewOrganizationHandlers(&fakeOrganizationStore{err: errors.New("database unavailable")}, NewResponder(nil))
	assertHandlerResponse(t, handlers.Organizations, http.MethodGet, "/orgs?admin=owner", "", http.StatusInternalServerError, `{"Message":"database unavailable"}`)
}

func TestOrganizationEndpointRepositoryFailure(t *testing.T) {
	handlers := NewOrganizationHandlers(&fakeOrganizationStore{err: errors.New("database unavailable")}, NewResponder(nil))
	assertHandlerResponse(t, handlers.Create, http.MethodPost, "/org?admin=owner", `{"org":"alpha"}`, http.StatusInternalServerError, `{"Message":"database unavailable"}`)
}

func TestCrossMatchTraitEndpointRepositoryFailure(t *testing.T) {
	handlers := NewOrganizationHandlers(&fakeOrganizationStore{err: errors.New("database unavailable")}, NewResponder(nil))
	assertHandlerResponse(t, handlers.CrossMatchTrait, http.MethodPost, "/crossmatchtrait?org=alpha", `{"trait":"x"}`, http.StatusInternalServerError, `{"Message":"database unavailable"}`)
}

func TestOrganizationHandlersRejectQueryEdgeCases(t *testing.T) {
	store := &fakeOrganizationStore{}
	handlers := NewOrganizationHandlers(store, NewResponder(nil))
	cases := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		target  string
		body    string
		status  int
		want    string
	}{
		{"orgs missing", handlers.Organizations, http.MethodGet, "/orgs", "", http.StatusBadRequest, `{"Message":"request query parameters must contain 'admin'"}`},
		{"orgs duplicate", handlers.Organizations, http.MethodGet, "/orgs?admin=a&admin=b", "", http.StatusBadRequest, `{"Message":"request query parameters must contain 'admin'"}`},
		{"org missing", handlers.Create, http.MethodPost, "/org", `{"org":"alpha"}`, http.StatusBadRequest, `{"Message":"request query parameters must contain 'admin'"}`},
		{"trait duplicate", handlers.CrossMatchTrait, http.MethodPost, "/crossmatchtrait?org=a&org=b", `{"trait":"x"}`, http.StatusBadRequest, `{"Message":"Request query parameters must contain org"}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assertHandlerResponse(t, test.handler, test.method, test.target, test.body, test.status, test.want)
		})
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestOrganizationHandlersValidateBodiesAndNullableTrait(t *testing.T) {
	store := &fakeOrganizationStore{}
	handlers := NewOrganizationHandlers(store, NewResponder(nil))
	assertHandlerResponse(t, handlers.Create, http.MethodPost, "/org?admin=owner", `{"org":`, http.StatusBadRequest, `{"Message":"unexpected end of JSON input"}`)
	assertHandlerResponse(t, handlers.Create, http.MethodPost, "/org?admin=owner", `{"org":""}`, http.StatusInternalServerError, `{"Message":"Organization name cannot be an empty string"}`)
	assertHandlerResponse(t, handlers.CrossMatchTrait, http.MethodPost, "/crossmatchtrait?org=alpha", `{"trait":`, http.StatusBadRequest, `{"Message":"Request body is malformed"}`)
	assertHandlerResponse(t, handlers.CrossMatchTrait, http.MethodPost, "/crossmatchtrait?org=alpha", `{"trait":null}`, http.StatusOK, `{"Message":"Successfully set the cross match trait"}`)
	if store.setTrait != nil {
		t.Fatalf("nullable trait = %v, want nil", store.setTrait)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
}

func TestOrganizationHandlersRejectOversizedBodiesBeforeStoreMutation(t *testing.T) {
	store := &fakeOrganizationStore{}
	handlers := NewOrganizationHandlers(store, NewResponder(nil))
	handlers.maxBodyBytes = 1

	assertHandlerResponse(t, handlers.Create, http.MethodPost, "/org?admin=owner", `{"org":"alpha"}`, http.StatusBadRequest, `{"Message":"http: request body too large"}`)
	assertHandlerResponse(t, handlers.CrossMatchTrait, http.MethodPost, "/crossmatchtrait?org=alpha", `{"trait":"cohort"}`, http.StatusBadRequest, `{"Message":"Malformed body."}`)
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestOrganizationHandlersMapEmptyResults(t *testing.T) {
	store := &fakeOrganizationStore{}
	handlers := NewOrganizationHandlers(store, NewResponder(nil))
	store.organizations = nil
	assertHandlerResponse(t, handlers.Organizations, http.MethodGet, "/orgs?admin=owner", "", http.StatusOK, `{"orgs":[]}`)
}
