package handlers

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnamadeo/mealbot/internal/store"
)

const (
	// MaxMemory : Max. amount of memory when parsing multipart form in the HTTP header
	MaxMemory = 32 << 20
	// FileFlag :
	FileFlag = os.O_WRONLY | os.O_CREATE
	// ReadWritePermissions :
	ReadWritePermissions = 0666
)

// CreateMembersResponse :
type CreateMembersResponse struct {
	Members []store.MemberResponse `json:"members"`
	Traits  []string               `json:"traits"`
}

// GetMembersResponse :
type GetMembersResponse struct {
	Members         []store.MemberResponse `json:"members"`
	Traits          []string               `json:"traits"`
	CrossMatchTrait string                 `json:"crossMatchTrait"`
}

// extractTraits : Returns the trait names (every key except name/email) of the first member
func extractTraits(members []store.MemberResponse) []string {
	traits := []string{}
	if len(members) > 0 {
		for trait := range members[0] {
			if trait != "name" && trait != "email" {
				traits = append(traits, trait)
			}
		}
	}
	return traits
}

// Members : Combined handlers for create and retrieving members
func (h *Handler) Members(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		h.CreateMembers(w, r)
	} else if r.Method == "GET" {
		h.GetMembers(w, r)
	}
}

// GetMembers : HTTP handler for retrieving members
func (h *Handler) GetMembers(w http.ResponseWriter, r *http.Request) {
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

	members, err := h.Store.GetActiveMembersAsMaps(r.Context(), orgname)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	traits := extractTraits(members)

	crossMatchTrait, err := h.Store.GetCrossMatchTrait(r.Context(), orgname)
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

// CreateMembers : HTTP handler for creating members
func (h *Handler) CreateMembers(w http.ResponseWriter, r *http.Request) {
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

	filename := filepath.Join(h.Config.CSVDir, handler.Filename)

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

	members, err := h.CreateMembersFromCSV(r.Context(), orgname, filename)
	if err != nil {
		LogAndWriteStatusInternalServerError(w, err, function)
		return
	}

	traits := extractTraits(members)

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

// ParseMembersCSV parses a members CSV (which must have a 'name' column and
// may have an 'email' column and any number of metadata columns).
func ParseMembersCSV(orgname string, in io.Reader) ([]store.Member, error) {
	reader := csv.NewReader(bufio.NewReader(in))
	members := []store.Member{}

	headers := []string{}

	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		if len(headers) == 0 {
			if isValidFormatCSV(row) {
				for _, val := range row {
					headers = append(headers, strings.ToLower(val))
				}
				continue
			}
			return nil, errors.New("CSV must have a column titled 'name'")
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

		members = append(members, store.Member{
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

	return members, nil
}

// CreateMembersFromCSV reads the CSV at filename, saves its members in the DB
// and returns them in response format.
func (h *Handler) CreateMembersFromCSV(ctx context.Context, orgname string, filename string) ([]store.MemberResponse, error) {
	file, err := os.Open(filename)
	if err != nil {
		return []store.MemberResponse{}, err
	}
	defer file.Close()

	members, err := ParseMembersCSV(orgname, file)
	if err != nil {
		return []store.MemberResponse{}, err
	}

	err = h.Store.SaveMembers(ctx, orgname, members)
	if err != nil {
		return []store.MemberResponse{}, err
	}

	var membersJSON []store.MemberResponse
	for _, member := range members {
		memberJSON := member.Metadata
		memberJSON["name"] = member.Name
		memberJSON["email"] = member.Email
		membersJSON = append(membersJSON, memberJSON)
	}

	return membersJSON, nil
}
