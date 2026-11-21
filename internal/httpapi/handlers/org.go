package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/db"
)

// CreateOrganizationRequestBody :
type CreateOrganizationRequestBody struct {
	Organization string `json:"org"`
}

// SetCrossMatchTraitRequestBody :
type SetCrossMatchTraitRequestBody struct {
	Trait string `json:"trait"`
}

// GetOrganizations : HTTP Handler for fetching all the organizations an admin manages
func (h *Handler) GetOrganizations(w http.ResponseWriter, r *http.Request) {
	function := "GetOrganizationsHandler"
	if r.Method != "GET" && r.Method != "" {
		LogAndWriteErr(
			w,
			errors.New("Only GET requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	admin, err := getQueryParam(r, "admin")
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	organizations, err := h.Store.GetOrganizations(r.Context(), admin)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	resp := map[string][]string{"orgs": organizations}
	bytes, err := json.Marshal(resp)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	LogAndWrite(w, bytes, http.StatusOK, function)
}

// CreateOrganization : HTTP handler for creating a new organization
func (h *Handler) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	function := "CreateOrganizationHandler"
	if r.Method != "POST" {
		LogAndWriteErr(
			w,
			errors.New("Only POST requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	bytes, err := io.ReadAll(r.Body)
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}
	defer r.Body.Close()

	var body CreateOrganizationRequestBody
	err = json.Unmarshal(bytes, &body)
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	admin, err := getQueryParam(r, "admin")
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	slog.Debug("creating organization", "org", body.Organization, "admin", admin)

	err = h.Store.CreateOrganization(r.Context(), body.Organization, admin)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	LogAndWrite(
		w,
		db.StrToBytes("Successfully created new organization"),
		http.StatusCreated,
		function,
	)
}

// CrossMatchTrait : HTTP handler for changing or setting a cross match trait for an organization
func (h *Handler) CrossMatchTrait(w http.ResponseWriter, r *http.Request) {
	function := "CrossMatchTraitHandler"
	if r.Method != "POST" {
		LogAndWriteErr(w, errors.New("Only POST requests are allowed at this route"), http.StatusMethodNotAllowed, function)
		return
	}

	orgname, err := getQueryParam(r, "org")
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	bytes, err := io.ReadAll(r.Body)
	if err != nil {
		LogAndWriteErr(w, errors.New("Malformed body."), http.StatusBadRequest, function)
		return
	}
	defer r.Body.Close()

	var body SetCrossMatchTraitRequestBody
	err = json.Unmarshal(bytes, &body)
	if err != nil {
		LogAndWriteErr(w, errors.New("Request body is malformed"), http.StatusBadRequest, function)
		return
	}

	err = h.Store.SetCrossMatchTrait(r.Context(), orgname, body.Trait)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(db.ErrToBytes(err))
		return
	}

	LogAndWrite(w, db.StrToBytes("Successfully set the cross match trait"), http.StatusCreated, function)
}
