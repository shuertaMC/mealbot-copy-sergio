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

	"github.com/johnamadeo/mealbot/internal/store"
)

type fakeMemberStore struct {
	members   []store.Member
	plan      store.MemberSyncPlan
	listErr   error
	syncErr   error
	listCalls int
	syncCalls int
	source    string
}

func (s *fakeMemberStore) Members(_ context.Context, _ string, _ bool) ([]store.Member, error) {
	s.listCalls++
	return s.members, s.listErr
}

func (s *fakeMemberStore) SynchronizeMembersCSV(_ context.Context, organization string, source io.Reader) (store.MemberSyncPlan, error) {
	s.syncCalls++
	contents, err := io.ReadAll(source)
	if err != nil {
		return store.MemberSyncPlan{}, err
	}
	s.source = string(contents)
	if s.syncErr != nil {
		return store.MemberSyncPlan{}, s.syncErr
	}
	if s.plan.Organization == "" {
		s.plan.Organization = organization
	}
	return s.plan, nil
}

type fakeTraitReader struct {
	trait *string
	err   error
	calls int
}

func (s *fakeTraitReader) CrossMatchTrait(_ context.Context, _ string) (*string, error) {
	s.calls++
	return s.trait, s.err
}

func multipartRequest(t *testing.T, target, field, filename, contents string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestMemberGetSuccessEmptyAndOptionalTrait(t *testing.T) {
	trait := "cohort"
	metadata := map[string]string{"year": "2024", "cohort": "blue"}
	members := &fakeMemberStore{members: []store.Member{{Name: "Alice", Email: "alice@example.com", Metadata: metadata}}}
	traits := &fakeTraitReader{trait: &trait}
	handlers := NewMemberHandlers(members, traits, NewResponder(nil))
	assertHandlerResponse(t, handlers.Get, http.MethodGet, "/members?org=alpha", "", http.StatusOK, `{"members":[{"cohort":"blue","email":"alice@example.com","name":"Alice","year":"2024"}],"traits":["cohort","year"],"crossMatchTrait":"cohort"}`)
	if !reflect.DeepEqual(metadata, map[string]string{"year": "2024", "cohort": "blue"}) {
		t.Fatalf("store metadata mutated: %#v", metadata)
	}

	members.members = nil
	traits.trait = nil
	assertHandlerResponse(t, handlers.Get, http.MethodGet, "/members?org=empty", "", http.StatusOK, `{"members":[],"traits":[]}`)

	emptyTrait := ""
	traits.trait = &emptyTrait
	assertHandlerResponse(t, handlers.Get, http.MethodGet, "/members?org=empty", "", http.StatusOK, `{"members":[],"traits":[],"crossMatchTrait":""}`)
}

func TestMemberGetFailuresAndMethodContracts(t *testing.T) {
	members := &fakeMemberStore{}
	traits := &fakeTraitReader{}
	handlers := NewMemberHandlers(members, traits, NewResponder(nil))
	assertHandlerResponse(t, handlers.Get, http.MethodPost, "/members?org=alpha", "", http.StatusMethodNotAllowed, `{"Message":"Only GET requests are allowed at this route"}`)
	assertHandlerResponse(t, handlers.Get, http.MethodGet, "/members", "", http.StatusBadRequest, `{"Message":"Request query parameters must contain org"}`)
	assertHandlerResponse(t, handlers.Get, http.MethodGet, "/members?org=a&org=b", "", http.StatusBadRequest, `{"Message":"Request query parameters must contain org"}`)

	members.listErr = errors.New("database unavailable")
	assertHandlerResponse(t, handlers.Get, http.MethodGet, "/members?org=alpha", "", http.StatusInternalServerError, `{"Message":"database unavailable"}`)
	members.listErr = nil
	traits.err = errors.New("trait unavailable")
	assertHandlerResponse(t, handlers.Get, http.MethodGet, "/members?org=alpha", "", http.StatusInternalServerError, `{"Message":"trait unavailable"}`)

	recorder := httptest.NewRecorder()
	handlers.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/members?org=alpha", nil))
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("unsupported dispatcher response = (%d, %q)", recorder.Code, recorder.Body.String())
	}
}

func TestMemberCreateSuccessAndStoreFailure(t *testing.T) {
	csv := "Name,Email,Year,Cohort\n Alice , alice@example.com ,2024,blue\n"
	members := &fakeMemberStore{plan: store.MemberSyncPlan{Members: []store.MemberInput{{
		Name: "Alice", Email: "alice@example.com", Metadata: map[string]string{"year": "2024", "cohort": "blue"},
	}}}}
	handlers := NewMemberHandlers(members, &fakeTraitReader{}, NewResponder(nil))

	recorder := httptest.NewRecorder()
	handlers.Create(recorder, multipartRequest(t, "/members?org=alpha", "members", "people.csv", csv))
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"members":[{"cohort":"blue","email":"alice@example.com","name":"Alice","year":"2024"}],"traits":["cohort","year"]}` {
		t.Fatalf("success response = (%d, %q)", recorder.Code, recorder.Body.String())
	}
	if members.syncCalls != 1 || members.source != csv {
		t.Fatalf("sync = (%d, %q)", members.syncCalls, members.source)
	}

	members.syncErr = errors.New("database unavailable")
	recorder = httptest.NewRecorder()
	handlers.Create(recorder, multipartRequest(t, "/members?org=alpha", "members", "people.csv", csv))
	if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != `{"Message":"database unavailable"}` {
		t.Fatalf("store error response = (%d, %q)", recorder.Code, recorder.Body.String())
	}
}

func TestMemberCreateRejectsMalformedRequestsBeforeStoreMutation(t *testing.T) {
	members := &fakeMemberStore{}
	handlers := NewMemberHandlers(members, &fakeTraitReader{}, NewResponder(nil))
	assertHandlerResponse(t, handlers.Create, http.MethodGet, "/members?org=alpha", "", http.StatusMethodNotAllowed, `{"Message":"Only POST requests are allowed at this route"}`)
	assertHandlerResponse(t, handlers.Create, http.MethodPost, "/members", "", http.StatusBadRequest, `{"Message":"Request query parameters must contain org"}`)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/members?org=alpha", strings.NewReader("not multipart"))
	request.Header.Set("Content-Type", "multipart/form-data")
	handlers.Create(recorder, request)
	if recorder.Code != http.StatusBadRequest || recorder.Body.String() != `{"Message":"no multipart boundary param in Content-Type"}` {
		t.Fatalf("malformed multipart = (%d, %q)", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handlers.Create(recorder, multipartRequest(t, "/members?org=alpha", "wrong", "people.csv", "Name,Email\nAlice,a@example.com\n"))
	if recorder.Code != http.StatusBadRequest || recorder.Body.String() != `{"Message":"http: no such file"}` {
		t.Fatalf("missing field = (%d, %q)", recorder.Code, recorder.Body.String())
	}

	invalidCSVs := []string{
		"Email\na@example.com\n",
		"Name\nAlice\n",
		"Name,Email\nAlice\n",
		"Name,Email\n",
	}
	for _, contents := range invalidCSVs {
		recorder = httptest.NewRecorder()
		handlers.Create(recorder, multipartRequest(t, "/members?org=alpha", "members", "people.csv", contents))
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("invalid CSV %q status = %d, body %q", contents, recorder.Code, recorder.Body.String())
		}
	}
	if members.syncCalls != 0 {
		t.Fatalf("sync calls = %d, want 0", members.syncCalls)
	}
}

func TestMemberCreateRemovesMultipartSpillFile(t *testing.T) {
	csv := "Name,Email\nAlice,alice@example.com\n"
	members := &fakeMemberStore{plan: store.MemberSyncPlan{Members: []store.MemberInput{{Name: "Alice", Email: "alice@example.com"}}}}
	handlers := NewMemberHandlers(members, &fakeTraitReader{}, NewResponder(nil))
	handlers.maxMemory = 1
	request := multipartRequest(t, "/members?org=alpha", "members", "people.csv", csv)
	recorder := httptest.NewRecorder()
	handlers.Create(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", recorder.Code, recorder.Body.String())
	}
	files := request.MultipartForm.File["members"]
	if len(files) != 1 {
		t.Fatalf("multipart files = %d", len(files))
	}
	if file, err := files[0].Open(); err == nil {
		_ = file.Close()
		t.Fatal("multipart spill file still opens after handler cleanup")
	}
}
