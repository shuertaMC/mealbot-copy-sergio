package httpapi

import (
	"errors"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/server"
)

// CreateOrganizationRequestBody :
type CreateOrganizationRequestBody struct {
	Organization string `json:"org"`
}

// SetCrossMatchTraitRequestBody :
type SetCrossMatchTraitRequestBody struct {
	Trait string `json:"trait"`
}

var errMissingAdmin = errors.New("request query parameters must contain 'admin'")

func getAdminParam(r *http.Request) (string, error) {
	admin, ok := singleQueryValue(r, "admin")
	if !ok {
		return "", errMissingAdmin
	}
	return admin, nil
}

// getOrganizations : GET /orgs?admin=... fetches all the organizations an admin manages
func (s *Server) getOrganizations(w http.ResponseWriter, r *http.Request) {
	function := "GetOrganizationsHandler"

	admin, err := getAdminParam(r)
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	organizations, err := s.store.GetOrganizations(r.Context(), admin)
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	s.writeJSON(w, map[string][]string{"orgs": organizations}, function)
}

// createOrganization : POST /org?admin=... creates a new organization
func (s *Server) createOrganization(w http.ResponseWriter, r *http.Request) {
	function := "CreateOrganizationHandler"

	var body CreateOrganizationRequestBody
	if err := decodeBody(r, &body); err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	admin, err := getAdminParam(r)
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	if err := s.store.CreateOrganization(r.Context(), body.Organization, admin); err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	s.writeOK(w, server.StrToBytes("Successfully created new organization"), function)
}

// setCrossMatchTrait : POST /crossmatchtrait?org=... changes or sets the cross match trait of an organization
func (s *Server) setCrossMatchTrait(w http.ResponseWriter, r *http.Request) {
	function := "CrossMatchTraitHandler"

	orgname, err := getQueryParam(r, "org")
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	var body SetCrossMatchTraitRequestBody
	if err := decodeBody(r, &body); err != nil {
		s.writeBadRequest(w, errors.New("Request body is malformed"), function)
		return
	}

	if err := s.store.SetCrossMatchTrait(r.Context(), orgname, body.Trait); err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	s.writeOK(w, server.StrToBytes("Successfully set the cross match trait"), function)
}
