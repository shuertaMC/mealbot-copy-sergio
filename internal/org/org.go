// Package org implements the organization handlers and queries (organizations
// and their cross-match trait).
package org

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/db"
	"github.com/johnamadeo/mealbot/internal/httputil"
)

// Organization :
type Organization struct {
	Name            string
	Admin           string
	CrossMatchTrait string
}

// CreateOrganizationRequestBody :
type CreateOrganizationRequestBody struct {
	Organization string `json:"org"`
}

// SetCrossMatchTraitRequestBody :
type SetCrossMatchTraitRequestBody struct {
	Trait string `json:"trait"`
}

// Handler serves the organization routes using DB for persistence.
type Handler struct {
	DB db.Querier
}

// NewHandler returns a Handler backed by q.
func NewHandler(q db.Querier) *Handler {
	return &Handler{DB: q}
}

// GetOrganizationsHandler : HTTP Handler for fetching all the organizations an admin manages
func (h *Handler) GetOrganizationsHandler(w http.ResponseWriter, r *http.Request) {
	function := "GetOrganizationsHandler"
	if r.Method != "GET" && r.Method != "" {
		httputil.LogAndWriteErr(
			w,
			errors.New("Only GET requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	admin, err := httputil.GetQueryParam(r, "admin")
	if err != nil {
		httputil.LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	organizations, err := GetOrganizations(h.DB, admin)
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	resp := map[string][]string{"orgs": organizations}
	bytes, err := json.Marshal(resp)
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	httputil.LogAndWrite(w, bytes, http.StatusOK, function)
}

// CreateOrganizationHandler : HTTP handler for creating a new organization
func (h *Handler) CreateOrganizationHandler(w http.ResponseWriter, r *http.Request) {
	function := "CreateOrganizationHandler"
	if r.Method != "POST" {
		httputil.LogAndWriteErr(
			w,
			errors.New("Only POST requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	bytes, err := io.ReadAll(r.Body)
	if err != nil {
		httputil.LogAndWriteStatusBadRequest(w, err, function)
		return
	}
	defer r.Body.Close()

	var body CreateOrganizationRequestBody
	err = json.Unmarshal(bytes, &body)
	if err != nil {
		httputil.LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	admin, err := httputil.GetQueryParam(r, "admin")
	if err != nil {
		httputil.LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	fmt.Println(body.Organization, admin)

	err = CreateOrganization(h.DB, body.Organization, admin)
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	httputil.LogAndWrite(
		w,
		httputil.StrToBytes("Successfully created new organization"),
		http.StatusCreated,
		function,
	)
}

// CrossMatchTraitHandler : HTTP handler for changing or setting a cross match trait for an organization
func (h *Handler) CrossMatchTraitHandler(w http.ResponseWriter, r *http.Request) {
	function := "CrossMatchTraitHandler"
	if r.Method != "POST" {
		httputil.LogAndWriteErr(w, errors.New("Only POST requests are allowed at this route"), http.StatusMethodNotAllowed, function)
		return
	}

	orgname, err := httputil.GetQueryParam(r, "org")
	if err != nil {
		httputil.LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	bytes, err := io.ReadAll(r.Body)
	if err != nil {
		httputil.LogAndWriteErr(w, errors.New("Malformed body."), http.StatusBadRequest, function)
		return
	}
	defer r.Body.Close()

	var body SetCrossMatchTraitRequestBody
	err = json.Unmarshal(bytes, &body)
	if err != nil {
		httputil.LogAndWriteErr(w, errors.New("Request body is malformed"), http.StatusBadRequest, function)
		return
	}

	err = SetCrossMatchTrait(h.DB, orgname, body.Trait)
	if err != nil {
		httputil.LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	httputil.LogAndWrite(w, httputil.StrToBytes("Successfully set the cross match trait"), http.StatusCreated, function)
}

// GetOrganizations : names of the organizations managed by admin
func GetOrganizations(q db.Querier, admin string) ([]string, error) {
	return queryOrgNames(q, "SELECT name FROM organizations WHERE admin = $1", admin)
}

// GetAllOrganizations : names of every organization
func GetAllOrganizations(q db.Querier) ([]string, error) {
	return queryOrgNames(q, "SELECT name FROM organizations")
}

// queryOrgNames runs a query returning a single organization name column.
func queryOrgNames(q db.Querier, query string, args ...any) ([]string, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	organizations := []string{}
	for rows.Next() {
		var organization string
		if err := rows.Scan(&organization); err != nil {
			return nil, err
		}

		organizations = append(organizations, organization)
	}

	return organizations, nil
}

// CreateOrganization inserts a new organization managed by admin.
func CreateOrganization(q db.Querier, name string, admin string) error {
	if name == "" {
		return errors.New("Organization name cannot be an empty string")
	}

	_, err := q.Exec(
		"INSERT INTO organizations (name, admin) VALUES ($1, $2)",
		name,
		admin,
	)
	if err != nil {
		return err
	}

	return nil
}

// GetCrossMatchTrait returns the organization's cross match trait ("" if unset).
func GetCrossMatchTrait(q db.Querier, orgname string) (string, error) {
	rows, err := q.Query(
		"SELECT cross_match_trait FROM organizations WHERE name = $1",
		orgname,
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var crossMatchTraitSQL sql.NullString
	for rows.Next() {
		err := rows.Scan(&crossMatchTraitSQL)
		if err != nil {
			return "", err
		}
		break
	}

	crossMatchTrait := ""
	if crossMatchTraitSQL.Valid {
		crossMatchTrait = crossMatchTraitSQL.String
	}

	return crossMatchTrait, nil
}

// SetCrossMatchTrait sets the organization's cross match trait.
func SetCrossMatchTrait(q db.Querier, orgname string, crossMatchTrait string) error {
	_, err := q.Exec(
		"UPDATE organizations SET cross_match_trait = $1 WHERE name = $2",
		crossMatchTrait,
		orgname,
	)
	if err != nil {
		return err
	}

	return nil
}
