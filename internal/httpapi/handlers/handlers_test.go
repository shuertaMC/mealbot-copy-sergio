package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/mail"
)

// failingQuerier is a fake db.Querier whose every call fails.
type failingQuerier struct{ err error }

type failingRow struct{ err error }

func (r failingRow) Scan(dest ...any) error { return r.err }

func (f failingQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, f.err
}

func (f failingQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, f.err
}

func (f failingQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return failingRow{f.err}
}

func newTestHandler(err error) *Handler {
	return New(failingQuerier{err}, &mail.Fake{}, config.Config{CSVDir: "."})
}

func do(h http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func message(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q is not a JSON message: %v", rec.Body.String(), err)
	}
	return m["Message"]
}

func TestMethodNotAllowed(t *testing.T) {
	h := newTestHandler(errors.New("db down"))
	cases := []struct {
		name   string
		fn     http.HandlerFunc
		method string
	}{
		{"GetMembers", h.GetMembers, "POST"},
		{"CreateMembers", h.CreateMembers, "GET"},
		{"GetOrganizations", h.GetOrganizations, "POST"},
		{"CreateOrganization", h.CreateOrganization, "GET"},
		{"CrossMatchTrait", h.CrossMatchTrait, "GET"},
		{"GetRounds", h.GetRounds, "POST"},
		{"AddRound", h.AddRound, "GET"},
		{"RemoveRound", h.RemoveRound, "GET"},
		{"Round", h.Round, "PUT"},
		{"GetPairs", h.GetPairs, "POST"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(c.fn, c.method, "/x", "")
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("code = %d", rec.Code)
			}
			if message(t, rec) == "" {
				t.Fatal("empty message")
			}
		})
	}
}

func TestRescheduleRoundWrongMethod(t *testing.T) {
	h := newTestHandler(nil)
	rec := do(h.RescheduleRound, "GET", "/round", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestMembersUnsupportedMethodWritesNothing(t *testing.T) {
	h := newTestHandler(errors.New("db down"))
	rec := do(h.Members, "PUT", "/members?org=o", "")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestBadRequests(t *testing.T) {
	h := newTestHandler(errors.New("db down"))
	cases := []struct {
		name   string
		fn     http.HandlerFunc
		method string
		target string
		body   string
	}{
		{"members missing org", h.GetMembers, "GET", "/members", ""},
		{"members duplicate org", h.GetMembers, "GET", "/members?org=a&org=b", ""},
		{"orgs missing admin", h.GetOrganizations, "GET", "/orgs", ""},
		{"org bad json", h.CreateOrganization, "POST", "/org?admin=a", "{"},
		{"org missing admin", h.CreateOrganization, "POST", "/org", `{"org":"x"}`},
		{"trait missing org", h.CrossMatchTrait, "POST", "/crossmatchtrait", `{}`},
		{"trait bad json", h.CrossMatchTrait, "POST", "/crossmatchtrait?org=o", "nope"},
		{"rounds missing org", h.GetRounds, "GET", "/rounds", ""},
		{"add round missing round", h.AddRound, "POST", "/round?org=o", ""},
		{"remove round missing id", h.RemoveRound, "DELETE", "/round?org=o", ""},
		{"reschedule missing params", h.RescheduleRound, "POST", "/round?org=o&roundId=1", ""},
		{"pairs missing org", h.GetPairs, "GET", "/pairs", ""},
		{"create members missing org", h.CreateMembers, "POST", "/members", ""},
		{"create members not multipart", h.CreateMembers, "POST", "/members?org=o", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(c.fn, c.method, c.target, c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDatabaseErrorsAre500(t *testing.T) {
	h := newTestHandler(errors.New("db down"))
	cases := []struct {
		name   string
		fn     http.HandlerFunc
		method string
		target string
		body   string
	}{
		{"members", h.GetMembers, "GET", "/members?org=o", ""},
		{"orgs", h.GetOrganizations, "GET", "/orgs?admin=a", ""},
		{"create org", h.CreateOrganization, "POST", "/org?admin=a", `{"org":"x"}`},
		{"set trait", h.CrossMatchTrait, "POST", "/crossmatchtrait?org=o", `{"trait":"t"}`},
		{"rounds", h.GetRounds, "GET", "/rounds?org=o", ""},
		{"remove round", h.RemoveRound, "DELETE", "/round?org=o&roundId=1", ""},
		{"remove round non-numeric", h.RemoveRound, "DELETE", "/round?org=o&roundId=x", ""},
		{"reschedule", h.RescheduleRound, "POST", "/round?org=o&round=2019-01-01&roundId=1", ""},
		{"pairs", h.GetPairs, "GET", "/pairs?org=o", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(c.fn, c.method, c.target, c.body)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAddRoundDatabaseErrorIs400(t *testing.T) {
	// The original implementation reports AddRound failures as 400.
	h := newTestHandler(errors.New("db down"))
	rec := do(h.AddRound, "POST", "/round?org=o&round=2019-01-01", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestCreateOrganizationEmptyNameIs500(t *testing.T) {
	h := newTestHandler(nil)
	rec := do(h.CreateOrganization, "POST", "/org?admin=a", `{"org":""}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestCors(t *testing.T) {
	called := false
	h := Cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	req := httptest.NewRequest("OPTIONS", "/members", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called {
		t.Fatal("preflight must not reach the wrapped handler")
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" ||
		rec.Header().Get("Access-Control-Allow-Headers") != AccessControlAllowHeaders ||
		rec.Header().Get("Access-Control-Allow-Methods") != "GET, POST, DELETE" {
		t.Fatalf("headers: %v", rec.Header())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/members", nil))
	if !called {
		t.Fatal("GET should reach the wrapped handler")
	}
}

func TestLogAndWriteKeepsImplicit200(t *testing.T) {
	rec := httptest.NewRecorder()
	LogAndWrite(rec, []byte("x"), http.StatusCreated, "f")
	if rec.Code != http.StatusOK || rec.Body.String() != "x" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestParseMembersCSV(t *testing.T) {
	in := "Name,Email,Year\n Alice ,a@x.com,2019\nBob,b@x.com,2020\n"
	members, err := ParseMembersCSV("org", strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %+v", members)
	}
	a := members[0]
	if a.Name != "Alice" || a.Email != "a@x.com" || a.Metadata["year"] != "2019" || a.Organization != "org" {
		t.Fatalf("unexpected member %+v", a)
	}
	if a.LastRoundWith["b@x.com"] != -1 || len(a.LastRoundWith) != 1 {
		t.Fatalf("unexpected last round with %+v", a.LastRoundWith)
	}
}

func TestParseMembersCSVRequiresNameColumn(t *testing.T) {
	_, err := ParseMembersCSV("org", strings.NewReader("email,year\na@x.com,2019\n"))
	if err == nil || !strings.Contains(err.Error(), "'name'") {
		t.Fatalf("err = %v", err)
	}
}
