package httpapi

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/logging"
	"github.com/johnamadeo/mealbot/internal/store"
)

func allowAll(h http.Handler) http.Handler { return h }

// denyAll is a fake auth middleware that rejects everything but preflights.
func denyAll(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			h.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
}

func newTestHandler(t *testing.T, staticDir string, auth func(http.Handler) http.Handler) http.Handler {
	t.Helper()
	// no database: these tests only exercise paths that never reach it
	srv := NewServer(nil, config.Config{StaticDir: staticDir}, logging.Discard())
	return srv.Routes(Middleware{MiddlewareHandlers: []func(http.Handler) http.Handler{auth, CORS}})
}

func do(h http.Handler, method, target, contentType string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Golden responses of every route for requests that fail validation.
func TestRouteValidationResponses(t *testing.T) {
	const (
		missingOrg   = `{"Message":"Request query parameters must contain org"}`
		missingOrgs  = `{"Message":"Request query parameters does not contain org"}`
		missingAdmin = `{"Message":"request query parameters must contain 'admin'"}`
		onlyGET      = `{"Message":"Only GET requests are allowed at this route"}`
		onlyPOST     = `{"Message":"Only POST requests are allowed at this route"}`
	)

	tests := []struct {
		name   string
		method string
		target string
		body   string
		status int
		resp   string
	}{
		{"members get without org", "GET", "/members", "", 400, missingOrg},
		{"members post without org", "POST", "/members", "", 400, missingOrg},
		{"members put", "PUT", "/members", "", 405, `{"Message":"Only GET and POST requests are allowed at this route"}`},
		{"members delete", "DELETE", "/members?org=ysc", "", 405, `{"Message":"Only GET and POST requests are allowed at this route"}`},
		{"orgs without admin", "GET", "/orgs", "", 400, missingAdmin},
		{"orgs with two admins", "GET", "/orgs?admin=a&admin=b", "", 400, missingAdmin},
		{"orgs post", "POST", "/orgs", "", 405, onlyGET},
		{"org get", "GET", "/org", "", 405, onlyPOST},
		{"org without admin", "POST", "/org", `{"org":"ysc"}`, 400, missingAdmin},
		{"crossmatchtrait get", "GET", "/crossmatchtrait", "", 405, onlyPOST},
		{"crossmatchtrait without org", "POST", "/crossmatchtrait", "", 400, missingOrg},
		{"crossmatchtrait malformed body", "POST", "/crossmatchtrait?org=ysc", "nope", 400, `{"Message":"Request body is malformed"}`},
		{"rounds post", "POST", "/rounds", "", 405, onlyGET},
		{"rounds without org", "GET", "/rounds", "", 400, missingOrg},
		{"round put", "PUT", "/round", "", 405, `{"Message":"Only POST and DELETE requests are allowed at this route"}`},
		{"round get", "GET", "/round", "", 405, `{"Message":"Only POST and DELETE requests are allowed at this route"}`},
		{"round add without org", "POST", "/round", "", 400, missingOrgs},
		{"round add without date", "POST", "/round?org=ysc", "", 400, `{"Message":"Request query parameters does not contain round"}`},
		{"round reschedule without org", "POST", "/round?roundId=1", "", 400, missingOrgs},
		{"round delete without params", "DELETE", "/round", "", 400, missingOrgs},
		{"round delete without id", "DELETE", "/round?org=ysc", "", 400, `{"Message":"Request query parameters does not contain roundId"}`},
		{"round delete with bad id", "DELETE", "/round?org=ysc&roundId=abc", "", 500, `{"Message":"strconv.Atoi: parsing \"abc\": invalid syntax"}`},
		{"pairs post", "POST", "/pairs", "", 405, onlyGET},
		{"pairs without org", "GET", "/pairs", "", 400, missingOrg},
	}

	h := newTestHandler(t, t.TempDir(), allowAll)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(h, tc.method, tc.target, "", []byte(tc.body))
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if rec.Body.String() != tc.resp {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.resp)
			}
		})
	}
}

func TestMethodNotAllowedSetsAllowHeader(t *testing.T) {
	h := newTestHandler(t, t.TempDir(), allowAll)
	rec := do(h, "PUT", "/round", "", nil)
	if got := rec.Header().Get("Allow"); got != "POST, DELETE" {
		t.Errorf("Allow = %q", got)
	}
}

func TestMalformedJSONBodiesAreBadRequests(t *testing.T) {
	h := newTestHandler(t, t.TempDir(), allowAll)
	if rec := do(h, "POST", "/org?admin=a", "", []byte("not json")); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}

func multipartBody(t *testing.T, field, filename, content string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if field != "" {
		part, err := mw.CreateFormFile(field, filename)
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte(content))
	}
	mw.Close()
	return buf.Bytes(), mw.FormDataContentType()
}

func TestCreateMembersUploadValidation(t *testing.T) {
	h := newTestHandler(t, t.TempDir(), allowAll)

	// not a multipart request
	if rec := do(h, "POST", "/members?org=ysc", "text/plain", []byte("x")); rec.Code != http.StatusBadRequest {
		t.Errorf("non-multipart status = %d", rec.Code)
	}

	// multipart without the 'members' file
	body, ct := multipartBody(t, "other", "m.csv", "Name\nA")
	if rec := do(h, "POST", "/members?org=ysc", ct, body); rec.Code != http.StatusBadRequest {
		t.Errorf("missing file status = %d", rec.Code)
	}

	// CSV without a name column
	body, ct = multipartBody(t, "members", "m.csv", "Email\na@x.com\n")
	rec := do(h, "POST", "/members?org=ysc", ct, body)
	if rec.Code != http.StatusBadRequest || rec.Body.String() != `{"Message":"CSV must have a column titled 'name'"}` {
		t.Errorf("bad csv: %d %s", rec.Code, rec.Body.String())
	}
}

func TestParseMembersCSV(t *testing.T) {
	members, err := parseMembersCSV("ysc", strings.NewReader("Name,Email,College,Year\nAnn A, ann@x.com,SM,2019\nBob B,bob@x.com,JE,2020\n"))
	if err != nil {
		t.Fatal(err)
	}

	want := []store.Member{
		{Organization: "ysc", Name: "Ann A", Email: "ann@x.com", Metadata: map[string]string{"college": "SM", "year": "2019"}},
		{Organization: "ysc", Name: "Bob B", Email: "bob@x.com", Metadata: map[string]string{"college": "JE", "year": "2020"}},
	}
	if !reflect.DeepEqual(members, want) {
		t.Errorf("members = %+v, want %+v", members, want)
	}

	for name, input := range map[string]string{
		"empty":             "",
		"header only":       "Name,Email\n",
		"no name column":    "Email\na@x.com\n",
		"ragged rows":       "Name,Email\nAnn\n",
	} {
		if _, err := parseMembersCSV("ysc", strings.NewReader(input)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestTraitsOf(t *testing.T) {
	got := traitsOf(MemberResponse{"name": "A", "email": "a@x.com", "year": "1", "college": "SM"})
	if !reflect.DeepEqual(got, []string{"college", "year"}) {
		t.Errorf("traits = %v", got)
	}
}

func TestHealthzIsUnauthenticated(t *testing.T) {
	h := newTestHandler(t, t.TempDir(), denyAll)

	rec := do(h, "GET", "/healthz", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("healthz = %d %q", rec.Code, rec.Body.String())
	}
	if rec := do(h, "POST", "/healthz", "", nil); rec.Code == http.StatusOK {
		t.Errorf("POST healthz should not succeed, got %d", rec.Code)
	}
}

func TestAuthMiddlewareGuardsAPIRoutes(t *testing.T) {
	h := newTestHandler(t, t.TempDir(), denyAll)

	for _, tc := range []struct{ method, target string }{
		{"GET", "/members?org=ysc"}, {"POST", "/members?org=ysc"}, {"GET", "/orgs?admin=a"},
		{"POST", "/org?admin=a"}, {"POST", "/crossmatchtrait?org=ysc"}, {"GET", "/rounds?org=ysc"},
		{"POST", "/round?org=ysc"}, {"DELETE", "/round?org=ysc&roundId=0"}, {"GET", "/pairs?org=ysc"},
		{"PUT", "/members"},
	} {
		req := httptest.NewRequest(tc.method, tc.target, nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", tc.method, tc.target, rec.Code)
		}
		// CORS is the outermost layer, so browsers can read the 401
		if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
			t.Errorf("%s %s: missing CORS header on 401", tc.method, tc.target)
		}
	}
}

func TestPreflightRequests(t *testing.T) {
	h := newTestHandler(t, t.TempDir(), denyAll)

	for _, path := range []string{"/members", "/orgs", "/org", "/crossmatchtrait", "/rounds", "/round", "/pairs"} {
		req := httptest.NewRequest("OPTIONS", path, nil)
		req.Header.Set("Origin", "http://localhost:3000")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Errorf("OPTIONS %s: %d %q", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" ||
			rec.Header().Get("Access-Control-Allow-Methods") != "GET, POST, DELETE" ||
			rec.Header().Get("Access-Control-Allow-Headers") != AccessControlAllowHeaders {
			t.Errorf("OPTIONS %s: unexpected CORS headers %v", path, rec.Header())
		}
	}
}

func TestStaticFilesAreServedWithoutAuth(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "privacy.html"), []byte("<p>privacy</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newTestHandler(t, dir, denyAll)

	rec := do(h, "GET", "/privacy.html", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "<p>privacy</p>" {
		t.Errorf("static = %d %q", rec.Code, rec.Body.String())
	}
	if rec := do(h, "GET", "/missing.html", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing static status = %d", rec.Code)
	}
}

func TestMiddlewareApplyOrder(t *testing.T) {
	var order []string
	mk := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	mw := Middleware{MiddlewareHandlers: []func(http.Handler) http.Handler{mk("auth"), mk("cors")}}
	h := mw.Apply(func(w http.ResponseWriter, r *http.Request) { order = append(order, "core") })
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	// handlers are wrapped in list order, so the last one listed runs first
	if strings.Join(order, ",") != "cors,auth,core" {
		t.Errorf("order = %v", order)
	}
}

func TestParseMembersCSVFixtures(t *testing.T) {
	file, err := os.Open("../../testdata/csv/test_john3.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	members, err := parseMembersCSV("ysc", file)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 4 || members[3].Email != "jadk@telkom.net" || members[3].Metadata["college"] != "SM" {
		t.Errorf("unexpected members: %+v", members)
	}
}
