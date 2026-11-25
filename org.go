package mealbot

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/httpapi"
	"github.com/johnamadeo/server"
)

// Organization :
type Organization struct {
	Name            string
	Admin           string
	CrossMatchTrait string
}

var getOrganizationsForHandler = getOrganizations
var createOrganizationForHandler = createOrganization
var setCrossMatchTraitForHandler = setCrossMatchTrait

type legacyOrganizationHTTPStore struct{}

func (legacyOrganizationHTTPStore) OrganizationsByAdmin(_ context.Context, admin string) ([]string, error) {
	return getOrganizationsForHandler(admin)
}

func (legacyOrganizationHTTPStore) CreateOrganization(_ context.Context, name, admin string) error {
	return createOrganizationForHandler(name, admin)
}

func (legacyOrganizationHTTPStore) CrossMatchTrait(_ context.Context, organization string) (*string, error) {
	trait, err := getCrossMatchTraitForHandler(organization)
	if err != nil {
		return nil, err
	}
	// The legacy database function collapses SQL NULL to an empty string, so
	// this bridge keeps the field present for compatibility.
	return &trait, nil
}

func (legacyOrganizationHTTPStore) SetCrossMatchTrait(_ context.Context, organization string, trait *string) error {
	value := ""
	if trait != nil {
		value = *trait
	}
	return setCrossMatchTraitForHandler(organization, value)
}

func legacyOrganizationHandlers() httpapi.OrganizationHandlers {
	return httpapi.NewOrganizationHandlers(legacyOrganizationHTTPStore{}, legacyResponder)
}

// GetOrganizationsHandler : HTTP Handler for fetching all the organizations an admin manages
func GetOrganizationsHandler(w http.ResponseWriter, r *http.Request) {
	legacyOrganizationHandlers().Organizations(w, r)
}

// CreateOrganizationHandler : HTTP handler for creating a new organization
func CreateOrganizationHandler(w http.ResponseWriter, r *http.Request) {
	legacyOrganizationHandlers().Create(w, r)
}

// CrossMatchTraitHandler : HTTP handler for changing or setting a cross match trait for an organization
func CrossMatchTraitHandler(w http.ResponseWriter, r *http.Request) {
	legacyOrganizationHandlers().CrossMatchTrait(w, r)
}

// GetOrganizations :
func getOrganizations(admin string) ([]string, error) {
	db, err := server.CreateDBConnection(LocalDBConnection)
	defer db.Close()
	if err != nil {
		return []string{}, err
	}

	rows, err := db.Query(
		"SELECT name FROM organizations WHERE admin = $1",
		admin,
	)
	if err != nil {
		return []string{}, err
	}

	organizations := []string{}
	for rows.Next() {
		var organization string
		err := rows.Scan(&organization)
		if err != nil {
			return []string{}, err
		}

		organizations = append(organizations, organization)
	}

	return organizations, nil
}

func createOrganization(name string, admin string) error {
	if name == "" {
		return errors.New("Organization name cannot be an empty string")
	}

	db, err := server.CreateDBConnection(LocalDBConnection)
	defer db.Close()
	if err != nil {
		return err
	}

	_, err = db.Exec(
		"INSERT INTO organizations (name, admin) VALUES ($1, $2)",
		name,
		admin,
	)
	if err != nil {
		return err
	}

	return nil
}

// GetCrossMatchTrait : Placeholder
func GetCrossMatchTrait(orgname string) (string, error) {
	db, err := server.CreateDBConnection(LocalDBConnection)
	defer db.Close()
	if err != nil {
		return "", err
	}

	rows, err := db.Query(
		"SELECT cross_match_trait FROM organizations WHERE name = $1",
		orgname,
	)
	if err != nil {
		return "", err
	}

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

func setCrossMatchTrait(orgname string, crossMatchTrait string) error {
	db, err := server.CreateDBConnection(LocalDBConnection)
	defer db.Close()
	if err != nil {
		return err
	}

	_, err = db.Exec(
		"UPDATE organizations SET cross_match_trait = $1 WHERE name = $2",
		crossMatchTrait,
		orgname,
	)
	if err != nil {
		return err
	}

	return nil
}
