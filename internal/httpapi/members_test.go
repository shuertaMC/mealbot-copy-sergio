package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/johnamadeo/mealbot/internal/domain"
)

func TestParseMembersCSVValidMetadataNormalizedHeadersAndHistories(t *testing.T) {
	t.Parallel()
	input := " Name , EMAIL , College , Year \nZed,z@example.com,SM,2020\nAmy,a@example.com,DC,2021\n"
	members, err := ParseMembersCSV(strings.NewReader(input), " org ", int64(len(input)))
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %#v", members)
	}
	if members[0].Organization != "org" || members[0].Metadata["college"] != "SM" || members[0].Metadata["year"] != "2020" {
		t.Fatalf("first member = %#v", members[0])
	}
	if !reflect.DeepEqual(members[0].LastRoundWith, map[string]int{"a@example.com": -1}) ||
		!reflect.DeepEqual(members[1].LastRoundWith, map[string]int{"z@example.com": -1}) {
		t.Fatalf("histories = %#v, %#v", members[0].LastRoundWith, members[1].LastRoundWith)
	}
}

func TestParseMembersCSVRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		limit int64
		kind  error
	}{
		{name: "empty", input: "", kind: domain.ErrValidation},
		{name: "header only", input: "name,email\n", kind: domain.ErrValidation},
		{name: "missing name", input: "email\na@example.com\n", kind: domain.ErrValidation},
		{name: "missing email", input: "name\nAmy\n", kind: domain.ErrValidation},
		{name: "duplicate normalized header", input: "name, Name,email\nAmy,Amy,a@example.com\n", kind: domain.ErrValidation},
		{name: "blank trait header", input: "name,email, \nAmy,a@example.com,x\n", kind: domain.ErrValidation},
		{name: "reserved trait", input: "name,email,organization\nAmy,a@example.com,x\n", kind: domain.ErrValidation},
		{name: "unequal short row", input: "name,email,team\nAmy,a@example.com\n", kind: domain.ErrValidation},
		{name: "unequal long row", input: "name,email\nAmy,a@example.com,x\n", kind: domain.ErrValidation},
		{name: "blank name", input: "name,email\n ,a@example.com\n", kind: domain.ErrValidation},
		{name: "blank email", input: "name,email\nAmy, \n", kind: domain.ErrValidation},
		{name: "invalid email", input: "name,email\nAmy,not-an-email\n", kind: domain.ErrValidation},
		{name: "duplicate email", input: "name,email\nAmy,A@example.com\nAnn,a@example.com\n", kind: domain.ErrConflict},
		{name: "malformed quote", input: "name,email\n\"Amy,a@example.com\n", kind: domain.ErrValidation},
		{name: "oversized", input: "name,email\nAmy,a@example.com\n", limit: 3, kind: domain.ErrValidation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			limit := test.limit
			if limit == 0 {
				limit = int64(len(test.input))
			}
			_, err := ParseMembersCSV(strings.NewReader(test.input), "org", limit)
			if !errors.Is(err, test.kind) {
				t.Fatalf("error = %v; want %v", err, test.kind)
			}
		})
	}
}

type fakeMemberRepository struct {
	members       []domain.Member
	trait         *string
	listErr       error
	traitErr      error
	reconcileErr  error
	imported      []domain.Member
	organization  string
	activeOnly    bool
	reconcileOut  []domain.Member
	reconcileCall int
}

func (f *fakeMemberRepository) ListMembers(_ context.Context, organization string, activeOnly bool) ([]domain.Member, error) {
	f.organization, f.activeOnly = organization, activeOnly
	return f.members, f.listErr
}

func (f *fakeMemberRepository) CrossMatchTrait(context.Context, string) (*string, error) {
	return f.trait, f.traitErr
}

func (f *fakeMemberRepository) ReconcileMembers(_ context.Context, organization string, imported []domain.Member) ([]domain.Member, error) {
	f.organization = organization
	f.reconcileCall++
	f.imported = append([]domain.Member(nil), imported...)
	return f.reconcileOut, f.reconcileErr
}

func TestMemberServiceGetDeterministicOrderingAndNullableTrait(t *testing.T) {
	t.Parallel()
	repository := &fakeMemberRepository{members: []domain.Member{
		{Name: "Zed", Email: "z@example.com", Metadata: map[string]string{"year": "2020"}},
		{Name: "Amy", Email: "a@example.com", Metadata: map[string]string{"college": "DC"}},
	}}
	result, err := NewMemberService(repository).Get(context.Background(), " org ")
	if err != nil {
		t.Fatal(err)
	}
	if repository.organization != "org" || !repository.activeOnly {
		t.Fatalf("list input = %q, %t", repository.organization, repository.activeOnly)
	}
	if result.CrossMatchTrait != nil || !reflect.DeepEqual(result.Traits, []string{"college", "year"}) ||
		len(result.Members) != 2 || result.Members[0]["name"] != "Amy" {
		t.Fatalf("result = %#v", result)
	}
}

func TestMemberServiceImportPassesFullSetAndPropagatesRollbackError(t *testing.T) {
	t.Parallel()
	input := "name,email,team\nAmy,a@example.com,A\nZed,z@example.com,Z\n"
	repository := &fakeMemberRepository{reconcileOut: []domain.Member{
		{Name: "Zed", Email: "z@example.com", Metadata: map[string]string{"team": "Z"}},
		{Name: "Amy", Email: "a@example.com", Metadata: map[string]string{"team": "A"}},
	}}
	result, err := NewMemberService(repository).Import(context.Background(), "org", strings.NewReader(input), int64(len(input)))
	if err != nil {
		t.Fatal(err)
	}
	if repository.reconcileCall != 1 || len(repository.imported) != 2 || repository.imported[0].LastRoundWith["z@example.com"] != -1 {
		t.Fatalf("reconcile input = %#v", repository.imported)
	}
	if len(result.Members) != 2 || result.Members[0]["name"] != "Amy" || !reflect.DeepEqual(result.Traits, []string{"team"}) {
		t.Fatalf("result = %#v", result)
	}

	repository.reconcileErr = errors.New("transaction rollback failed")
	result, err = NewMemberService(repository).Import(context.Background(), "org", strings.NewReader(input), int64(len(input)))
	if err == nil || result.Members != nil {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

type fakeMemberUseCases struct {
	getResult    GetMembersResponse
	importResult CreateMembersResponse
	err          error
	organization string
	imported     string
}

func (f *fakeMemberUseCases) Get(_ context.Context, organization string) (GetMembersResponse, error) {
	f.organization = organization
	return f.getResult, f.err
}

func (f *fakeMemberUseCases) Import(_ context.Context, organization string, reader io.Reader, _ int64) (CreateMembersResponse, error) {
	f.organization = organization
	contents, _ := io.ReadAll(reader)
	f.imported = string(contents)
	return f.importResult, f.err
}

func TestMembersHandlerGetContracts(t *testing.T) {
	t.Parallel()
	trait := "college"
	tests := []struct {
		name   string
		method string
		url    string
		err    error
		status int
		body   string
	}{
		{name: "success", method: http.MethodGet, url: "/members?org=alpha", status: 200, body: "{\"members\":[{\"email\":\"a@example.com\",\"name\":\"Amy\"}],\"traits\":[],\"crossMatchTrait\":\"college\"}\n"},
		{name: "missing", method: http.MethodGet, url: "/members", status: 400, body: "{\"error\":\"invalid request\"}\n"},
		{name: "repeated", method: http.MethodGet, url: "/members?org=a&org=b", status: 400, body: "{\"error\":\"invalid request\"}\n"},
		{name: "empty", method: http.MethodGet, url: "/members?org=", status: 400, body: "{\"error\":\"invalid request\"}\n"},
		{name: "not found", method: http.MethodGet, url: "/members?org=alpha", err: domain.NewNotFoundError("org", "alpha"), status: 404, body: "{\"error\":\"not found\"}\n"},
		{name: "internal", method: http.MethodGet, url: "/members?org=alpha", err: errors.New("sql secret"), status: 500, body: "{\"error\":\"internal server error\"}\n"},
		{name: "method", method: http.MethodDelete, url: "/members?org=alpha", status: 405, body: "{\"error\":\"method not allowed\"}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeMemberUseCases{getResult: GetMembersResponse{Members: []MemberResponse{{"name": "Amy", "email": "a@example.com"}}, Traits: []string{}, CrossMatchTrait: &trait}, err: test.err}
			response := httptest.NewRecorder()
			NewMembersHandler(service, 4096, testLogger()).ServeHTTP(response, httptest.NewRequest(test.method, test.url, nil))
			assertHTTPResponse(t, response, test.status, test.body, "application/json; charset=utf-8")
			if strings.Contains(response.Body.String(), "sql secret") {
				t.Fatal("internal error leaked")
			}
		})
	}
}

func TestMembersHandlerPostContracts(t *testing.T) {
	t.Parallel()
	csvBody := "name,email\nAmy,a@example.com\n"
	tests := []struct {
		name        string
		field       string
		contents    string
		contentType string
		err         error
		limit       int64
		status      int
	}{
		{name: "success", field: "members", contents: csvBody, limit: 4096, status: 201},
		{name: "missing field", field: "other", contents: csvBody, limit: 4096, status: 400},
		{name: "malformed multipart", contentType: "multipart/form-data; boundary=x", contents: "--x\r\nbroken", limit: 4096, status: 400},
		{name: "oversized", field: "members", contents: csvBody, limit: 4, status: 400},
		{name: "validation", field: "members", contents: "bad", err: domain.NewValidationError("csv", "bad"), limit: 4096, status: 400},
		{name: "repository", field: "members", contents: csvBody, err: errors.New("transaction details"), limit: 4096, status: 500},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var body bytes.Buffer
			contentType := test.contentType
			if contentType == "" {
				writer := multipart.NewWriter(&body)
				part, err := writer.CreateFormFile(test.field, "../../untrusted.csv")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = part.Write([]byte(test.contents))
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				contentType = writer.FormDataContentType()
			} else {
				body.WriteString(test.contents)
			}
			service := &fakeMemberUseCases{importResult: CreateMembersResponse{Members: []MemberResponse{{"name": "Amy", "email": "a@example.com"}}, Traits: []string{}}, err: test.err}
			request := httptest.NewRequest(http.MethodPost, "/members?org=alpha", &body)
			request.Header.Set("Content-Type", contentType)
			response := httptest.NewRecorder()
			NewMembersHandler(service, test.limit, testLogger()).ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("status=%d type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
			if test.status == 201 {
				want := "{\"members\":[{\"email\":\"a@example.com\",\"name\":\"Amy\"}],\"traits\":[]}\n"
				if response.Body.String() != want || service.organization != "alpha" || service.imported != csvBody {
					t.Fatalf("body=%q org=%q import=%q", response.Body.String(), service.organization, service.imported)
				}
			}
			if strings.Contains(response.Body.String(), "transaction details") {
				t.Fatal("internal error leaked")
			}
		})
	}
}

func TestMembersHandlerPostQueryValidation(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"/members", "/members?org=", "/members?org=a&org=b"} {
		response := httptest.NewRecorder()
		NewMembersHandler(&fakeMemberUseCases{}, 1024, testLogger()).ServeHTTP(
			response,
			httptest.NewRequest(http.MethodPost, target, strings.NewReader("not multipart")),
		)
		if response.Code != http.StatusBadRequest || response.Body.String() != "{\"error\":\"invalid request\"}\n" {
			t.Fatalf("target %q: status=%d body=%q", target, response.Code, response.Body.String())
		}
	}
}

func TestMembersHandlerRejectsMalformedCSVBeforeReconciliation(t *testing.T) {
	t.Parallel()
	repository := &fakeMemberRepository{}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("members", "ignored.csv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("name,email\n\"unterminated,a@example.com\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/members?org=alpha", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	NewMembersHandler(NewMemberService(repository), 4096, testLogger()).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || repository.reconcileCall != 0 {
		t.Fatalf("status=%d body=%q reconciliation calls=%d", response.Code, response.Body.String(), repository.reconcileCall)
	}
}
