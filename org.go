package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/server"
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

// GetOrganizationsHandler : HTTP Handler for fetching all the organizations an admin manages
func GetOrganizationsHandler(w http.ResponseWriter, r *http.Request) {
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

	queries, ok := r.URL.Query()["admin"]
	if !ok || len(queries) > 1 {
		LogAndWriteErr(
			w,
			errors.New("request query parameters must contain 'admin'"),
			http.StatusBadRequest,
			function,
		)
		return
	}

	organizations, err := getOrganizations(queries[0])
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

// CreateOrganizationHandler : HTTP handler for creating a new organization
func CreateOrganizationHandler(w http.ResponseWriter, r *http.Request) {
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

	bytes, err := ioutil.ReadAll(r.Body)
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

	queries, ok := r.URL.Query()["admin"]
	if !ok || len(queries) > 1 {
		LogAndWriteErr(
			w,
			errors.New("request query parameters must contain 'admin'"),
			http.StatusBadRequest,
			function,
		)
		return
	}
	admin := queries[0]

	fmt.Println(body.Organization, admin)

	err = createOrganization(body.Organization, admin)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	LogAndWrite(
		w,
		server.StrToBytes("Successfully created new organization"),
		http.StatusCreated,
		function,
	)
}

// CrossMatchTraitHandler : HTTP handler for changing or setting a cross match trait for an organization
func CrossMatchTraitHandler(w http.ResponseWriter, r *http.Request) {
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

	bytes, err := ioutil.ReadAll(r.Body)
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

	err = setCrossMatchTrait(orgname, body.Trait)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(server.ErrToBytes(err))
		return
	}

	LogAndWrite(w, server.StrToBytes("Successfully set the cross match trait"), http.StatusCreated, function)
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
