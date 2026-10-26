package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnamadeo/mealbot/internal/server"
)

const (
	// MaxMemory : Max. amount of memory when parsing multipart form in the HTTP header
	MaxMemory = 32 << 20
	// CSVPath : Filepath for accessing local CSV files
	CSVPath = "./csv/"
	// FileFlag :
	FileFlag = os.O_WRONLY | os.O_CREATE
	// ReadWritePermissions :
	ReadWritePermissions = 0666
)

// Member :
type Member struct {
	Organization  string
	Email         string `json:"email"`
	Name          string `json:"name"`
	Metadata      map[string]string
	LastRoundWith map[string]int
}

// MemberResponse : Data structure for representing a member
type MemberResponse map[string]string

// CreateMembersResponse :
type CreateMembersResponse struct {
	Members []MemberResponse `json:"members"`
	Traits  []string         `json:"traits"`
}

// GetMembersResponse :
type GetMembersResponse struct {
	Members         []MemberResponse `json:"members"`
	Traits          []string         `json:"traits"`
	CrossMatchTrait string           `json:"crossMatchTrait"`
}

// MembersHandler : Combined handlers for create and retrieving members
func MembersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		CreateMembersHandler(w, r)
	} else if r.Method == "GET" {
		GetMembersHandler(w, r)
	}
}

// GetMembersHandler : HTTP handler for retrieving members
func GetMembersHandler(w http.ResponseWriter, r *http.Request) {
	function := "GetMembersHandler"
	if r.Method != "GET" {
		LogAndWriteErr(
			w,
			errors.New("Only GET requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	orgname, err := getQueryParam(r, "org")
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	members, err := getActiveMembersFromDBAsMap(orgname)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	traits := []string{}
	if len(members) > 0 {
		for trait := range members[0] {
			if trait != "name" && trait != "email" {
				traits = append(traits, trait)
			}
		}
	}

	crossMatchTrait, err := GetCrossMatchTrait(orgname)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	resp := GetMembersResponse{
		Members: members,
		Traits:  traits,
	}
	if crossMatchTrait != "" {
		resp.CrossMatchTrait = crossMatchTrait
	}

	bytes, err := json.Marshal(resp)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	LogAndWrite(w, bytes, http.StatusOK, function)
}

// TODO: The problem is here!
// CreateMembersHandler : HTTP handler for creating members
func CreateMembersHandler(w http.ResponseWriter, r *http.Request) {
	function := "CreateMembersHandler"
	if r.Method != "POST" {
		LogAndWriteErr(
			w,
			errors.New("Only POST requests are allowed at this route"),
			http.StatusMethodNotAllowed,
			function,
		)
		return
	}

	orgname, err := getQueryParam(r, "org")
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	err = r.ParseMultipartForm(MaxMemory)
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	formFile, handler, err := r.FormFile("members")
	if err != nil {
		LogAndWriteStatusBadRequest(w, err, function)
		return
	}

	defer formFile.Close()

	filename := filepath.Join(CSVPath, handler.Filename)

	file, err := os.OpenFile(filename, FileFlag, ReadWritePermissions)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}
	defer file.Close()

	_, err = io.Copy(file, formFile)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	members, err := createMembersFromCSV(orgname, filename)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	var traits []string
	for trait := range members[0] {
		if trait != "name" && trait != "email" {
			traits = append(traits, trait)
		}
	}

	resp := CreateMembersResponse{
		Members: members,
		Traits:  traits,
	}

	bytes, err := json.Marshal(resp)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	LogAndWrite(w, bytes, http.StatusCreated, function)
}

// isValidFormatCSV :
func isValidFormatCSV(headers []string) bool {
	for _, val := range headers {
		if strings.ToLower(val) == "name" {
			return true
		}
	}

	return false
}

func createMembersFromCSV(orgname string, filename string) ([]MemberResponse, error) {
	file, err := os.Open(filename)
	if err != nil {
		return []MemberResponse{}, err
	}

	reader := csv.NewReader(bufio.NewReader(file))
	members := []Member{}

	headers := []string{}

	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			return []MemberResponse{}, err
		}

		if len(headers) == 0 {
			if isValidFormatCSV(row) {
				for _, val := range row {
					headers = append(headers, strings.ToLower(val))
				}
				continue
			} else {
				err = errors.New("CSV must have a column titled 'name'")
				return []MemberResponse{}, err
			}
		}

		name := ""
		email := ""
		metadata := map[string]string{}
		for i, val := range row {
			if headers[i] == "name" {
				name = strings.Trim(val, " ")
			} else if headers[i] == "email" {
				email = strings.Trim(val, " ")
			} else {
				metadata[headers[i]] = val
			}
		}

		members = append(members, Member{
			Organization:  orgname, // Need to grab from HTTP request
			Email:         email,
			Name:          name,
			Metadata:      metadata,
			LastRoundWith: map[string]int{}, // fill in later
		})
	}

	for i := range members {
		for j := range members {
			if i == j {
				continue
			}
			members[i].LastRoundWith[members[j].Email] = -1
		}
	}

	err = saveMembersInDB(orgname, members)
	if err != nil {
		return []MemberResponse{}, err
	}

	var membersJSON []MemberResponse
	for _, member := range members {
		memberJSON := member.Metadata
		memberJSON["name"] = member.Name
		memberJSON["email"] = member.Email
		membersJSON = append(membersJSON, memberJSON)
	}

	return membersJSON, nil
}

// TODO: Use transactions!!! Current implementation is brittle since it does
// rollback if insertion of multiple members fails halfway
func saveMembersInDB(orgname string, newMembers []Member) error {
	db, err := server.CreateDBConnection(LocalDBConnection)
	defer db.Close()
	if err != nil {
		return err
	}

	newMembersMap := map[string]Member{}
	for _, member := range newMembers {
		newMembersMap[member.Email] = member
	}

	membersMap := map[string]Member{}
	members, err := GetMembersFromDB(orgname, false)
	if err != nil {
		return err
	}

	for _, member := range members {
		membersMap[member.Email] = member
	}

	// remove existing members that are not in new list
	for email := range membersMap {
		if _, ok := newMembersMap[email]; !ok {
			delete(membersMap, email)
		}
	}

	// update fields of existing member (if existing member changes name or metadata)
	for email := range membersMap {
		if _, ok := newMembersMap[email]; ok {
			membersMap[email] = Member{
				Organization:  membersMap[email].Organization,
				Email:         membersMap[email].Email,         // cannot be updated by user
				LastRoundWith: membersMap[email].LastRoundWith, // update later
				Name:          newMembersMap[email].Name,
				Metadata:      newMembersMap[email].Metadata,
			}
		}
	}

	// update pair counts of existing members with new members
	for email, member := range membersMap {
		for newEmail := range newMembersMap {
			// if new email is not existing member
			if _, ok := membersMap[newEmail]; !ok {
				member.LastRoundWith[newEmail] = -1
			}
		}

		membersMap[email] = member
	}

	// save new member
	for email := range newMembersMap {
		if _, ok := membersMap[email]; !ok {
			lastRoundWith := map[string]int{}

			for otherEmail := range newMembersMap {
				if otherEmail == email {
					continue
				}
				lastRoundWith[otherEmail] = -1
			}

			membersMap[email] = Member{
				Organization:  orgname,
				Name:          newMembersMap[email].Name,
				Email:         newMembersMap[email].Email,
				Metadata:      newMembersMap[email].Metadata,
				LastRoundWith: lastRoundWith,
			}
		}
	}

	existingMemberEmails := map[string]bool{}
	for _, member := range members {
		existingMemberEmails[member.Email] = true
	}

	for _, member := range membersMap {
		metadataBytes, err := json.Marshal(member.Metadata)
		if err != nil {
			return err
		}

		lastRoundWithBytes, err := json.Marshal(member.LastRoundWith)
		if err != nil {
			return err
		}

		// Add new member
		if _, ok := existingMemberEmails[member.Email]; !ok {
			columns := "(organization, email, name, metadata, last_round_with, active)"
			placeholders := "($1, $2, $3, $4, $5, $6)"
			_, err = db.Exec(
				fmt.Sprintf(
					"INSERT INTO members %s VALUES %s",
					columns,
					placeholders,
				),
				member.Organization,
				member.Email,
				member.Name,
				server.JSONB(metadataBytes),
				server.JSONB(lastRoundWithBytes),
				true,
			)

			if err != nil {
				return err
			}
			// Update existing member
		} else {
			_, err = db.Exec(
				"UPDATE members SET name = $1, metadata = $2, last_round_with = $3, active = $4 WHERE organization = $5 AND email = $6",
				member.Name,
				server.JSONB(metadataBytes),
				server.JSONB(lastRoundWithBytes),
				true,
				orgname,
				member.Email,
			)
		}
	}

	// Deactivate member (note we don't delete member from the DB)
	for email := range existingMemberEmails {
		if _, ok := membersMap[email]; !ok {
			_, err = db.Exec(
				"UPDATE members SET active = $1 WHERE organization = $2 AND email = $3",
				false,
				orgname,
				email,
			)
		}
	}

	return nil
}

func getActiveMembersFromDBAsMap(orgname string) ([]MemberResponse, error) {
	members, err := GetMembersFromDB(orgname, true)
	if err != nil {
		return []MemberResponse{}, err
	}

	mapMembers := []MemberResponse{}
	for _, member := range members {
		mapMember := member.Metadata
		mapMember["name"] = member.Name
		mapMember["email"] = member.Email
		mapMembers = append(mapMembers, mapMember)
	}

	return mapMembers, nil
}

func getActiveMembersFromDBInPairFormat(orgname string) ([]Member, error) {
	members, err := GetMembersFromDB(orgname, true)
	if err != nil {
		return []Member{}, err
	}

	pairMembers := []Member{}
	for _, member := range members {
		pairMembers = append(pairMembers, Member{
			Name:  member.Name,
			Email: member.Email,
		})
	}

	return pairMembers, nil
}

// GetMembersFromDB : Placeholder
func GetMembersFromDB(orgname string, onlyActive bool) ([]Member, error) {
	db, err := server.CreateDBConnection(LocalDBConnection)
	defer db.Close()
	if err != nil {
		return []Member{}, err
	}

	members := []Member{}

	rows, err := db.Query(
		"SELECT organization, name, email, metadata, last_round_with, active FROM members WHERE organization = $1 ORDER BY name",
		orgname,
	)
	if err != nil {
		return members, err
	}

	for rows.Next() {
		var organization, name, email string
		var metadataJSON, lastRoundWithJSON server.JSONB
		var active bool
		err := rows.Scan(&organization, &name, &email, &metadataJSON, &lastRoundWithJSON, &active)
		if err != nil {
			return members, err
		}

		if onlyActive && !active {
			continue
		}

		bytes, err := metadataJSON.MarshalJSON()
		if err != nil {
			return members, err
		}

		var metadata map[string]string
		err = json.Unmarshal(bytes, &metadata)
		if err != nil {
			return members, err
		}

		bytes, err = lastRoundWithJSON.MarshalJSON()
		if err != nil {
			return members, err
		}

		var lastRoundWith map[string]int
		err = json.Unmarshal(bytes, &lastRoundWith)
		if err != nil {
			return members, err
		}

		members = append(members, Member{
			Organization:  organization,
			Name:          name,
			Email:         email,
			Metadata:      metadata,
			LastRoundWith: lastRoundWith,
		})
	}

	return members, nil
}
