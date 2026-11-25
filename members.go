package mealbot

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnamadeo/mealbot/internal/httpapi"
	internalstore "github.com/johnamadeo/mealbot/internal/store"
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

// MemberResponse is retained as an alias for legacy persistence helpers and
// tests while the transport DTO is owned by internal/httpapi.
type MemberResponse = httpapi.MemberResponse

// Handler dependency seams keep the legacy HTTP contract testable without a
// live database. They deliberately default to the original implementations.
var getActiveMembersForHandler = getActiveMembersFromDBAsMap
var getCrossMatchTraitForHandler = GetCrossMatchTrait
var createMembersFromCSVForHandler = createMembersFromCSV

type legacyMemberHTTPStore struct {
	responder httpapi.Responder
	removeAll func(string) error
}

func (legacyMemberHTTPStore) Members(_ context.Context, organization string, onlyActive bool) ([]internalstore.Member, error) {
	if onlyActive {
		responses, err := getActiveMembersForHandler(organization)
		if err != nil {
			return nil, err
		}
		members := make([]internalstore.Member, 0, len(responses))
		for _, response := range responses {
			metadata := make(map[string]string, len(response))
			for key, value := range response {
				if key != "name" && key != "email" {
					metadata[key] = value
				}
			}
			members = append(members, internalstore.Member{
				Organization: organization,
				Name:         response["name"],
				Email:        response["email"],
				Metadata:     metadata,
				Active:       true,
			})
		}
		return members, nil
	}
	legacyMembers, err := GetMembersFromDB(organization, false)
	if err != nil {
		return nil, err
	}
	members := make([]internalstore.Member, 0, len(legacyMembers))
	for _, member := range legacyMembers {
		members = append(members, internalstore.Member{
			Organization:  member.Organization,
			Email:         member.Email,
			Name:          member.Name,
			Metadata:      member.Metadata,
			LastRoundWith: member.LastRoundWith,
			Active:        true,
		})
	}
	return members, nil
}

func (s legacyMemberHTTPStore) SynchronizeMembersCSV(_ context.Context, organization string, source io.Reader) (internalstore.MemberSyncPlan, error) {
	const function = "CreateMembersHandler"
	filename := "members.csv"
	if upload, ok := source.(interface{ UploadFilename() string }); ok {
		if base := filepath.Base(upload.UploadFilename()); base != "." && base != string(filepath.Separator) && base != "" {
			filename = base
		}
	}
	temporaryDirectory, err := os.MkdirTemp("", "mealbot-members-upload-")
	if err != nil {
		return internalstore.MemberSyncPlan{}, err
	}
	removeAll := s.removeAll
	if removeAll == nil {
		removeAll = os.RemoveAll
	}
	defer s.responder.Cleanup(function, "temporary member upload directory", func() error {
		return removeAll(temporaryDirectory)
	})
	path := filepath.Join(temporaryDirectory, filename)
	file, err := os.Create(path)
	if err != nil {
		return internalstore.MemberSyncPlan{}, err
	}
	if _, err := io.Copy(file, source); err != nil {
		s.responder.Cleanup(function, "temporary member upload file", file.Close)
		return internalstore.MemberSyncPlan{}, err
	}
	if err := file.Close(); err != nil {
		return internalstore.MemberSyncPlan{}, err
	}

	responses, err := createMembersFromCSVForHandler(organization, path)
	if err != nil {
		return internalstore.MemberSyncPlan{}, err
	}
	members := make([]internalstore.MemberInput, 0, len(responses))
	for _, response := range responses {
		metadata := make(map[string]string, len(response))
		for key, value := range response {
			if key != "name" && key != "email" {
				metadata[key] = value
			}
		}
		members = append(members, internalstore.MemberInput{
			Name:     response["name"],
			Email:    response["email"],
			Metadata: metadata,
		})
	}
	return internalstore.MemberSyncPlan{Organization: organization, Members: members}, nil
}

func legacyMemberHandlers() httpapi.MemberHandlers {
	return httpapi.NewMemberHandlers(legacyMemberHTTPStore{
		responder: legacyResponder,
		removeAll: os.RemoveAll,
	}, legacyOrganizationHTTPStore{}, legacyResponder)
}

// MembersHandler : Combined handlers for create and retrieving members
func MembersHandler(w http.ResponseWriter, r *http.Request) {
	legacyMemberHandlers().ServeHTTP(w, r)
}

// GetMembersHandler : HTTP handler for retrieving members
func GetMembersHandler(w http.ResponseWriter, r *http.Request) {
	legacyMemberHandlers().Get(w, r)
}

// TODO: The problem is here!
// CreateMembersHandler : HTTP handler for creating members
func CreateMembersHandler(w http.ResponseWriter, r *http.Request) {
	legacyMemberHandlers().Create(w, r)
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
	db, err := openLegacyDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

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
				string(metadataBytes),
				string(lastRoundWithBytes),
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
				string(metadataBytes),
				string(lastRoundWithBytes),
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
	db, err := openLegacyDatabase()
	if err != nil {
		return []Member{}, err
	}
	defer db.Close()

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
		var metadataJSON, lastRoundWithJSON []byte
		var active bool
		err := rows.Scan(&organization, &name, &email, &metadataJSON, &lastRoundWithJSON, &active)
		if err != nil {
			return members, err
		}

		if onlyActive && !active {
			continue
		}

		var metadata map[string]string
		err = json.Unmarshal(metadataJSON, &metadata)
		if err != nil {
			return members, err
		}

		var lastRoundWith map[string]int
		err = json.Unmarshal(lastRoundWithJSON, &lastRoundWith)
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
