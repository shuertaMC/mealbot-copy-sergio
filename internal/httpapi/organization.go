package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

var errEmptyOrganizationName = errors.New("Organization name cannot be an empty string")

// CreateOrganizationRequest is the exact legacy JSON request shape.
type CreateOrganizationRequest struct {
	Organization string `json:"org"`
}

// SetCrossMatchTraitRequest preserves the nullable organization trait. A nil
// value represents either a missing or explicit JSON null trait.
type SetCrossMatchTraitRequest struct {
	Trait *string `json:"trait"`
}

// OrganizationsResponse is the legacy organization-list response.
type OrganizationsResponse struct {
	Organizations []string `json:"orgs"`
}

// OrganizationHandlers owns the three organization HTTP endpoints.
type OrganizationHandlers struct {
	store        OrganizationStore
	responder    Responder
	maxBodyBytes int64
}

// NewOrganizationHandlers constructs organization handlers with no global
// persistence or logging dependencies.
func NewOrganizationHandlers(store OrganizationStore, responder Responder) OrganizationHandlers {
	return OrganizationHandlers{store: store, responder: responder, maxBodyBytes: maxRequestBodyBytes}
}

// Organizations handles GET /orgs.
func (h OrganizationHandlers) Organizations(w http.ResponseWriter, r *http.Request) {
	const function = "GetOrganizationsHandler"
	if r.Method != http.MethodGet && r.Method != "" {
		h.responder.WriteError(w, errOnlyGETRequests, http.StatusMethodNotAllowed, function)
		return
	}

	queries, ok := r.URL.Query()["admin"]
	if !ok || len(queries) > 1 {
		h.responder.WriteBadRequest(w, errors.New("request query parameters must contain 'admin'"), function)
		return
	}
	if h.store == nil {
		h.responder.WriteInternalServerError(w, errors.New("organization store is unavailable"), function)
		return
	}

	organizations, err := h.store.OrganizationsByAdmin(r.Context(), queries[0])
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	if organizations == nil {
		organizations = make([]string, 0)
	}
	encoded, err := json.Marshal(OrganizationsResponse{Organizations: organizations})
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	h.responder.Write(w, encoded, http.StatusOK, function)
}

// Create handles POST /org.
func (h OrganizationHandlers) Create(w http.ResponseWriter, r *http.Request) {
	const function = "CreateOrganizationHandler"
	if r.Method != http.MethodPost {
		h.responder.WriteError(w, errOnlyPOSTRequests, http.StatusMethodNotAllowed, function)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	contents, err := io.ReadAll(r.Body)
	if err != nil {
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	var body CreateOrganizationRequest
	if err := json.Unmarshal(contents, &body); err != nil {
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	queries, ok := r.URL.Query()["admin"]
	if !ok || len(queries) > 1 {
		h.responder.WriteBadRequest(w, errors.New("request query parameters must contain 'admin'"), function)
		return
	}
	if body.Organization == "" {
		h.responder.WriteInternalServerError(w, errEmptyOrganizationName, function)
		return
	}
	if h.store == nil {
		h.responder.WriteInternalServerError(w, errors.New("organization store is unavailable"), function)
		return
	}
	if err := h.store.CreateOrganization(r.Context(), body.Organization, queries[0]); err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	h.responder.Write(w, MessageBytes("Successfully created new organization"), http.StatusCreated, function)
}

// CrossMatchTrait handles POST /crossmatchtrait.
func (h OrganizationHandlers) CrossMatchTrait(w http.ResponseWriter, r *http.Request) {
	const function = "CrossMatchTraitHandler"
	if r.Method != http.MethodPost {
		h.responder.WriteError(w, errOnlyPOSTRequests, http.StatusMethodNotAllowed, function)
		return
	}
	organization, err := QueryParam(r, "org")
	if err != nil {
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	contents, err := io.ReadAll(r.Body)
	if err != nil {
		h.responder.WriteBadRequest(w, errors.New("Malformed body."), function)
		return
	}
	var body SetCrossMatchTraitRequest
	if err := json.Unmarshal(contents, &body); err != nil {
		h.responder.WriteBadRequest(w, errors.New("Request body is malformed"), function)
		return
	}
	if h.store == nil {
		h.responder.WriteInternalServerError(w, errors.New("organization store is unavailable"), function)
		return
	}
	if err := h.store.SetCrossMatchTrait(r.Context(), organization, body.Trait); err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	h.responder.Write(w, MessageBytes("Successfully set the cross match trait"), http.StatusCreated, function)
}
