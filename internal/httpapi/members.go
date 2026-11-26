package httpapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/johnamadeo/mealbot/internal/domain"
)

var reservedTraitHeaders = map[string]struct{}{
	"active":          {},
	"crossmatchtrait": {},
	"last_round_with": {},
	"lastroundwith":   {},
	"metadata":        {},
	"organization":    {},
}

// MemberResponse preserves the legacy flattened member shape: name, email,
// and every arbitrary metadata trait are keys in one JSON object.
type MemberResponse map[string]string

// CreateMembersResponse is returned after an atomic member import.
type CreateMembersResponse struct {
	Members []MemberResponse `json:"members"`
	Traits  []string         `json:"traits"`
}

// GetMembersResponse is returned for active-member reads. CrossMatchTrait is
// JSON null when the organization has no configured trait.
type GetMembersResponse struct {
	Members         []MemberResponse `json:"members"`
	Traits          []string         `json:"traits"`
	CrossMatchTrait *string          `json:"crossMatchTrait"`
}

// MemberRepository is the member persistence surface consumed by
// MemberService. ReconcileMembers is responsible for one atomic activation,
// update, history-initialization, and deactivation transaction.
type MemberRepository interface {
	ListMembers(context.Context, string, bool) ([]domain.Member, error)
	ReconcileMembers(context.Context, string, []domain.Member) ([]domain.Member, error)
	CrossMatchTrait(context.Context, string) (*string, error)
}

// MemberUseCases is the narrow handler-facing member contract.
type MemberUseCases interface {
	Get(context.Context, string) (GetMembersResponse, error)
	Import(context.Context, string, io.Reader, int64) (CreateMembersResponse, error)
}

// MemberService validates imports and converts domain members to stable legacy
// response shapes.
type MemberService struct {
	repository MemberRepository
}

// NewMemberService constructs member use cases over repository.
func NewMemberService(repository MemberRepository) *MemberService {
	return &MemberService{repository: repository}
}

// Get returns active organization members, their sorted trait union, and the
// nullable configured cross-match trait.
func (s *MemberService) Get(ctx context.Context, organization string) (GetMembersResponse, error) {
	organization = strings.TrimSpace(organization)
	if err := domain.RequireNonEmpty("member.organization", organization); err != nil {
		return GetMembersResponse{}, err
	}
	members, err := s.repository.ListMembers(ctx, organization, true)
	if err != nil {
		return GetMembersResponse{}, err
	}
	trait, err := s.repository.CrossMatchTrait(ctx, organization)
	if err != nil {
		return GetMembersResponse{}, err
	}
	responses, traits := memberResponses(members)
	return GetMembersResponse{Members: responses, Traits: traits, CrossMatchTrait: trait}, nil
}

// Import parses a bounded CSV stream and passes the complete imported active
// set to one atomic repository reconciliation. Repository failures are returned
// before any transport success is possible.
func (s *MemberService) Import(ctx context.Context, organization string, source io.Reader, maxBytes int64) (CreateMembersResponse, error) {
	members, err := ParseMembersCSV(source, organization, maxBytes)
	if err != nil {
		return CreateMembersResponse{}, err
	}
	reconciled, err := s.repository.ReconcileMembers(ctx, strings.TrimSpace(organization), members)
	if err != nil {
		return CreateMembersResponse{}, err
	}
	responses, traits := memberResponses(reconciled)
	return CreateMembersResponse{Members: responses, Traits: traits}, nil
}

// ParseMembersCSV parses one bounded CSV import from source without using or
// trusting an upload filename. Headers are case-insensitive and outer-space
// normalized; name and email must each appear exactly once.
func ParseMembersCSV(source io.Reader, organization string, maxBytes int64) ([]domain.Member, error) {
	organization = strings.TrimSpace(organization)
	if err := domain.RequireNonEmpty("member.organization", organization); err != nil {
		return nil, err
	}
	if source == nil {
		return nil, domain.NewValidationError("members", "CSV source is required")
	}
	maxBytes = maxRequestBytes(maxBytes)
	contents, err := io.ReadAll(io.LimitReader(source, maxBytes+1))
	if err != nil {
		return nil, domain.NewValidationError("members", "CSV could not be read")
	}
	if int64(len(contents)) > maxBytes {
		return nil, domain.NewValidationError("members", "CSV exceeds the request size limit")
	}
	reader := csv.NewReader(bytes.NewReader(contents))
	reader.FieldsPerRecord = -1
	headers, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return nil, domain.NewValidationError("members", "CSV must not be empty")
	}
	if err != nil {
		return nil, malformedCSVError(err)
	}
	headers, nameIndex, emailIndex, err := normalizeMemberHeaders(headers)
	if err != nil {
		return nil, err
	}

	members := make([]domain.Member, 0)
	emails := make(map[string]struct{})
	for rowNumber := 2; ; rowNumber++ {
		row, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, malformedCSVError(readErr)
		}
		if len(row) != len(headers) {
			return nil, domain.NewValidationError("members", fmt.Sprintf("CSV row %d has %d fields; expected %d", rowNumber, len(row), len(headers)))
		}
		name := strings.TrimSpace(row[nameIndex])
		email := strings.TrimSpace(row[emailIndex])
		metadata := make(map[string]string, len(headers)-2)
		for index, header := range headers {
			if index != nameIndex && index != emailIndex {
				metadata[header] = row[index]
			}
		}
		member, memberErr := domain.NewMember(organization, email, name, metadata, nil)
		if memberErr != nil {
			return nil, memberErr
		}
		emailKey := strings.ToLower(member.Email)
		if _, duplicate := emails[emailKey]; duplicate {
			return nil, domain.NewValidationError("members", "duplicate member email")
		}
		emails[emailKey] = struct{}{}
		members = append(members, member)
	}
	if len(members) == 0 {
		return nil, domain.NewValidationError("members", "CSV must contain at least one member")
	}
	initializeImportHistories(members)
	return members, nil
}

func normalizeMemberHeaders(raw []string) ([]string, int, int, error) {
	if len(raw) == 0 {
		return nil, 0, 0, domain.NewValidationError("members", "CSV header must not be empty")
	}
	headers := make([]string, len(raw))
	seen := make(map[string]struct{}, len(raw))
	nameIndex, emailIndex := -1, -1
	for index, value := range raw {
		header := strings.ToLower(strings.TrimSpace(value))
		if header == "" {
			return nil, 0, 0, domain.NewValidationError("members", "CSV trait headers must not be blank")
		}
		if _, duplicate := seen[header]; duplicate {
			return nil, 0, 0, domain.NewValidationError("members", "CSV headers must not be duplicated")
		}
		seen[header] = struct{}{}
		switch header {
		case "name":
			nameIndex = index
		case "email":
			emailIndex = index
		default:
			if _, reserved := reservedTraitHeaders[header]; reserved {
				return nil, 0, 0, domain.NewValidationError("members", "CSV contains a reserved trait header")
			}
		}
		headers[index] = header
	}
	if nameIndex < 0 || emailIndex < 0 {
		return nil, 0, 0, domain.NewValidationError("members", "CSV must contain exactly one name and email header")
	}
	return headers, nameIndex, emailIndex, nil
}

func malformedCSVError(err error) error {
	return domain.NewValidationError("members", fmt.Sprintf("CSV is malformed: %v", err))
}

func initializeImportHistories(members []domain.Member) {
	for index := range members {
		members[index].LastRoundWith = make(map[string]int, len(members)-1)
		for otherIndex := range members {
			if index != otherIndex {
				members[index].LastRoundWith[members[otherIndex].Email] = -1
			}
		}
	}
}

func memberResponses(members []domain.Member) ([]MemberResponse, []string) {
	ordered := make([]domain.Member, len(members))
	for index, member := range members {
		ordered[index] = member.Clone()
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Name == ordered[j].Name {
			return ordered[i].Email < ordered[j].Email
		}
		return ordered[i].Name < ordered[j].Name
	})
	traitSet := make(map[string]struct{})
	responses := make([]MemberResponse, 0, len(ordered))
	for _, member := range ordered {
		response := make(MemberResponse, len(member.Metadata)+2)
		for key, value := range member.Metadata {
			response[key] = value
			traitSet[key] = struct{}{}
		}
		response["name"] = member.Name
		response["email"] = member.Email
		responses = append(responses, response)
	}
	traits := make([]string, 0, len(traitSet))
	for trait := range traitSet {
		traits = append(traits, trait)
	}
	sort.Strings(traits)
	return responses, traits
}

// NewMembersHandler constructs authenticated-route behavior for GET and POST
// /members?org=. POST expects exactly one multipart field named "members" and
// buffers only within the configured total request ceiling.
func NewMembersHandler(service MemberUseCases, maxBodyBytes int64, logger *slog.Logger) http.Handler {
	responses := newResponder(logger)
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodPost {
			responses.methodNotAllowed(w, http.MethodGet, http.MethodPost)
			return
		}
		organization, err := QueryValue(request, "org")
		if err != nil {
			responses.failure(request.Context(), w, "members", err)
			return
		}
		if request.Method == http.MethodGet {
			result, err := service.Get(request.Context(), organization)
			if err != nil {
				responses.failure(request.Context(), w, "get members", err)
				return
			}
			responses.json(w, http.StatusOK, result)
			return
		}

		contents, err := requestPart(request, "members", maxBodyBytes)
		if err != nil {
			responses.failure(request.Context(), w, "import members", err)
			return
		}
		result, err := service.Import(request.Context(), organization, bytes.NewReader(contents), int64(len(contents)))
		if err != nil {
			responses.failure(request.Context(), w, "import members", err)
			return
		}
		responses.json(w, http.StatusCreated, result)
	})
}
