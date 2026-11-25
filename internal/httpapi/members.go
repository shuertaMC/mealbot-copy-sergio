package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"

	"github.com/johnamadeo/mealbot/internal/store"
)

const (
	maxMultipartMemory        = 32 << 20
	maxRequestBodyBytes int64 = 32 << 20
)

// MemberResponse preserves dynamic metadata while adding the two required
// member identity fields.
type MemberResponse map[string]string

// MembersResponse is shared by GET and POST. GET normalizes SQL NULL and a
// missing organization to an empty string; the pointer keeps the GET-only
// field omitted from POST responses.
type MembersResponse struct {
	Members         []MemberResponse `json:"members"`
	Traits          []string         `json:"traits"`
	CrossMatchTrait *string          `json:"crossMatchTrait,omitempty"`
}

// MemberHandlers owns GET/POST dispatch for /members.
type MemberHandlers struct {
	members       MemberStore
	organizations CrossMatchTraitReader
	responder     Responder
	maxMemory     int64
	maxBodyBytes  int64
}

// NewMemberHandlers constructs member handlers with explicit dependencies.
func NewMemberHandlers(members MemberStore, organizations CrossMatchTraitReader, responder Responder) MemberHandlers {
	return MemberHandlers{
		members:       members,
		organizations: organizations,
		responder:     responder,
		maxMemory:     maxMultipartMemory,
		maxBodyBytes:  maxRequestBodyBytes,
	}
}

// ServeHTTP preserves the legacy dispatcher, including its silent handling of
// methods other than GET and POST.
func (h MemberHandlers) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.Create(w, r)
	case http.MethodGet:
		h.Get(w, r)
	}
}

// Get handles GET /members.
func (h MemberHandlers) Get(w http.ResponseWriter, r *http.Request) {
	const function = "GetMembersHandler"
	if r.Method != http.MethodGet {
		h.responder.WriteError(w, errors.New("Only GET requests are allowed at this route"), http.StatusMethodNotAllowed, function)
		return
	}
	organization, err := QueryParam(r, "org")
	if err != nil {
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	if h.members == nil {
		h.responder.WriteInternalServerError(w, errors.New("member store is unavailable"), function)
		return
	}
	members, err := h.members.Members(r.Context(), organization, true)
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	responseMembers := memberResponses(members)
	traits := responseTraits(responseMembers)

	if h.organizations == nil {
		h.responder.WriteInternalServerError(w, errors.New("organization store is unavailable"), function)
		return
	}
	crossMatchTrait, err := h.organizations.CrossMatchTrait(r.Context(), organization)
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	if crossMatchTrait == nil {
		emptyTrait := ""
		crossMatchTrait = &emptyTrait
	}
	encoded, err := json.Marshal(MembersResponse{
		Members:         responseMembers,
		Traits:          traits,
		CrossMatchTrait: crossMatchTrait,
	})
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	h.responder.Write(w, encoded, http.StatusOK, function)
}

// Create handles POST /members. The complete upload is read and parsed before
// the synchronization dependency is invoked, so malformed CSV cannot cause a
// partial mutation even when the injected implementation is simplistic.
func (h MemberHandlers) Create(w http.ResponseWriter, r *http.Request) {
	const function = "CreateMembersHandler"
	if r.Method != http.MethodPost {
		h.responder.WriteError(w, errors.New("Only POST requests are allowed at this route"), http.StatusMethodNotAllowed, function)
		return
	}
	organization, err := QueryParam(r, "org")
	if err != nil {
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)
	if err := r.ParseMultipartForm(h.maxMemory); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			err = maxBytesError
		}
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	if r.MultipartForm != nil {
		defer h.responder.Cleanup(function, "multipart form", r.MultipartForm.RemoveAll)
	}
	formFile, _, err := r.FormFile("members")
	if err != nil {
		h.responder.WriteBadRequest(w, err, function)
		return
	}
	defer h.responder.Cleanup(function, "multipart file", formFile.Close)
	contents, err := io.ReadAll(formFile)
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	parsed, err := store.ParseMemberCSV(bytes.NewReader(contents))
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	if _, err := store.PlanMemberSynchronization(organization, parsed); err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	if h.members == nil {
		h.responder.WriteInternalServerError(w, errors.New("member store is unavailable"), function)
		return
	}
	plan, err := h.members.SynchronizeMembersCSV(r.Context(), organization, bytes.NewReader(contents))
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	responseMembers := memberInputResponses(plan.Members)
	encoded, err := json.Marshal(MembersResponse{
		Members: responseMembers,
		Traits:  responseTraits(responseMembers),
	})
	if err != nil {
		h.responder.WriteInternalServerError(w, err, function)
		return
	}
	h.responder.Write(w, encoded, http.StatusCreated, function)
}

func memberResponses(members []store.Member) []MemberResponse {
	response := make([]MemberResponse, 0, len(members))
	for _, member := range members {
		values := make(MemberResponse, len(member.Metadata)+2)
		for key, value := range member.Metadata {
			values[key] = value
		}
		values["name"] = member.Name
		values["email"] = member.Email
		response = append(response, values)
	}
	return response
}

func memberInputResponses(members []store.MemberInput) []MemberResponse {
	response := make([]MemberResponse, 0, len(members))
	for _, member := range members {
		values := make(MemberResponse, len(member.Metadata)+2)
		for key, value := range member.Metadata {
			values[key] = value
		}
		values["name"] = member.Name
		values["email"] = member.Email
		response = append(response, values)
	}
	return response
}

func responseTraits(members []MemberResponse) []string {
	traits := make([]string, 0)
	if len(members) == 0 {
		return traits
	}
	for trait := range members[0] {
		if trait != "name" && trait != "email" {
			traits = append(traits, trait)
		}
	}
	sort.Strings(traits)
	return traits
}
