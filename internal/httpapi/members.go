package httpapi

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/johnamadeo/mealbot/internal/store"
)

// MaxMemory : Max. amount of memory when parsing multipart form in the HTTP header
const MaxMemory = 32 << 20

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

// getMembers : GET /members?org=... retrieves the active members of an organization
func (s *Server) getMembers(w http.ResponseWriter, r *http.Request) {
	function := "GetMembersHandler"

	orgname, err := getQueryParam(r, "org")
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	members, err := s.store.GetMembers(r.Context(), orgname, true)
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	memberResponses := toMemberResponses(members)
	traits := []string{}
	if len(memberResponses) > 0 {
		traits = traitsOf(memberResponses[0])
	}

	crossMatchTrait, err := s.store.GetCrossMatchTrait(r.Context(), orgname)
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	bytes, err := json.Marshal(GetMembersResponse{
		Members:         memberResponses,
		Traits:          traits,
		CrossMatchTrait: crossMatchTrait,
	})
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	s.writeOK(w, bytes, function)
}

// createMembers : POST /members?org=... replaces the members of an organization with the ones in an uploaded CSV
func (s *Server) createMembers(w http.ResponseWriter, r *http.Request) {
	function := "CreateMembersHandler"

	orgname, err := getQueryParam(r, "org")
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	if err := r.ParseMultipartForm(MaxMemory); err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	formFile, _, err := r.FormFile("members")
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}
	defer formFile.Close()

	members, err := parseMembersCSV(orgname, formFile)
	if err != nil {
		s.writeBadRequest(w, err, function)
		return
	}

	if err := s.store.SaveMembers(r.Context(), orgname, members); err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	memberResponses := toMemberResponses(members)

	// stays nil (JSON null) when there are no traits, as it always has
	var traits []string
	if len(memberResponses) > 0 {
		traits = traitsOf(memberResponses[0])
		if len(traits) == 0 {
			traits = nil
		}
	}

	bytes, err := json.Marshal(CreateMembersResponse{
		Members: memberResponses,
		Traits:  traits,
	})
	if err != nil {
		s.writeInternalError(w, err, function)
		return
	}

	s.writeOK(w, bytes, function)
}

// traitsOf lists the keys of a member other than name and email, sorted.
func traitsOf(member MemberResponse) []string {
	traits := []string{}
	for trait := range member {
		if trait != "name" && trait != "email" {
			traits = append(traits, trait)
		}
	}
	sort.Strings(traits)
	return traits
}

func toMemberResponses(members []store.Member) []MemberResponse {
	responses := []MemberResponse{}
	for _, member := range members {
		response := MemberResponse{}
		for key, value := range member.Metadata {
			response[key] = value
		}
		response["name"] = member.Name
		response["email"] = member.Email
		responses = append(responses, response)
	}
	return responses
}

// isValidFormatCSV : a CSV is valid if one of its headers is 'name'
func isValidFormatCSV(headers []string) bool {
	for _, val := range headers {
		if strings.ToLower(val) == "name" {
			return true
		}
	}

	return false
}

// parseMembersCSV reads members from a CSV with a header row containing at
// least a 'name' column. 'email' is the member's id; any other column becomes metadata.
func parseMembersCSV(orgname string, input io.Reader) ([]store.Member, error) {
	reader := csv.NewReader(bufio.NewReader(input))
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
			if !isValidFormatCSV(row) {
				return nil, errors.New("CSV must have a column titled 'name'")
			}
			for _, val := range row {
				headers = append(headers, strings.ToLower(val))
			}
			continue
		}

		name := ""
		email := ""
		metadata := map[string]string{}
		for i, val := range row {
			switch headers[i] {
			case "name":
				name = strings.Trim(val, " ")
			case "email":
				email = strings.Trim(val, " ")
			default:
				metadata[headers[i]] = val
			}
		}

		members = append(members, store.Member{
			Organization: orgname,
			Email:        email,
			Name:         name,
			Metadata:     metadata,
		})
	}

	if len(headers) == 0 {
		return nil, errors.New("CSV must have a column titled 'name'")
	}
	if len(members) == 0 {
		return nil, errors.New("CSV must contain at least one member")
	}

	return members, nil
}
