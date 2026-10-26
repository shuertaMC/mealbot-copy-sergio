package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests pin the HTTP responses of the request-validation paths, which
// do not need a database.
func TestHandlerCharacterization(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		target  string
		body    string
		status  int
		resp    string
	}{
		{"members get without org", MembersHandler, "GET", "/members", "", 400, `{"Message":"Request query parameters must contain org"}`},
		{"members post without org", MembersHandler, "POST", "/members", "", 400, `{"Message":"Request query parameters must contain org"}`},
		{"members put is ignored", MembersHandler, "PUT", "/members", "", 200, ``},
		{"orgs without admin", GetOrganizationsHandler, "GET", "/orgs", "", 400, `{"Message":"request query parameters must contain 'admin'"}`},
		{"orgs post not allowed", GetOrganizationsHandler, "POST", "/orgs", "", 405, `{"Message":"Only GET requests are allowed at this route"}`},
		{"org get not allowed", CreateOrganizationHandler, "GET", "/org", "", 405, `{"Message":"Only POST requests are allowed at this route"}`},
		{"org without admin", CreateOrganizationHandler, "POST", "/org", `{"org":"ysc"}`, 400, `{"Message":"request query parameters must contain 'admin'"}`},
		{"crossmatchtrait get not allowed", CrossMatchTraitHandler, "GET", "/crossmatchtrait", "", 405, `{"Message":"Only POST requests are allowed at this route"}`},
		{"crossmatchtrait without org", CrossMatchTraitHandler, "POST", "/crossmatchtrait", "", 400, `{"Message":"Request query parameters must contain org"}`},
		{"crossmatchtrait malformed body", CrossMatchTraitHandler, "POST", "/crossmatchtrait?org=ysc", "nope", 400, `{"Message":"Request body is malformed"}`},
		{"rounds post not allowed", GetRoundsHandler, "POST", "/rounds", "", 405, `{"Message":"Only GET requests are allowed at this route"}`},
		{"rounds without org", GetRoundsHandler, "GET", "/rounds", "", 400, `{"Message":"Request query parameters must contain org"}`},
		{"round put not allowed", RoundHandler, "PUT", "/round", "", 405, `{"Message":"Only POST and DELETE requests are allowed at this route"}`},
		{"round add without org", RoundHandler, "POST", "/round", "", 400, `{"Message":"Request query parameters does not contain org"}`},
		{"round reschedule without org", RoundHandler, "POST", "/round?roundId=1", "", 400, `{"Message":"Request query parameters does not contain org"}`},
		{"round delete without params", RoundHandler, "DELETE", "/round", "", 400, `{"Message":"Request query parameters does not contain org"}`},
		{"pairs post not allowed", GetPairsHandler, "POST", "/pairs", "", 405, `{"Message":"Only GET requests are allowed at this route"}`},
		{"pairs without org", GetPairsHandler, "GET", "/pairs", "", 400, `{"Message":"Request query parameters must contain org"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			tc.handler(rec, req)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if rec.Body.String() != tc.resp {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.resp)
			}
		})
	}
}

func TestCORSCharacterization(t *testing.T) {
	called := false
	h := GetCorsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	req := httptest.NewRequest("OPTIONS", "/members", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Error("preflight request must not reach the wrapped handler")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("allow origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, DELETE" {
		t.Errorf("allow methods = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != AccessControlAllowHeaders {
		t.Errorf("allow headers = %q", got)
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
