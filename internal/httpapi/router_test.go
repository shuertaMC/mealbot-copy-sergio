package httpapi

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type routerOrganizations struct{}

func (routerOrganizations) ListOrganizationNames(context.Context, string) ([]string, error) {
	return []string{"o"}, nil
}
func (routerOrganizations) Create(context.Context, string, string) error              { return nil }
func (routerOrganizations) SetCrossMatchTrait(context.Context, string, *string) error { return nil }

type blockingOrganizations struct{ routerOrganizations }

func (blockingOrganizations) ListOrganizationNames(ctx context.Context, _ string) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type routerMembers struct{}

func (routerMembers) Get(context.Context, string) (GetMembersResponse, error) {
	return GetMembersResponse{Members: []MemberResponse{}, Traits: []string{}}, nil
}
func (routerMembers) Import(context.Context, string, io.Reader, int64) (CreateMembersResponse, error) {
	return CreateMembersResponse{Members: []MemberResponse{}, Traits: []string{}}, nil
}

type routerRounds struct{}

func (routerRounds) List(context.Context, string) ([]string, error)        { return []string{}, nil }
func (routerRounds) Add(context.Context, string, string) error             { return nil }
func (routerRounds) Reschedule(context.Context, string, int, string) error { return nil }
func (routerRounds) Remove(context.Context, string, int) error             { return nil }

type routerPairs struct{}

func (routerPairs) ListHistory(context.Context, string) (GetPairsResponse, error) {
	return GetPairsResponse{RoundPairs: [][]GetPairsResponsePair{}}, nil
}

func newTestRouter(t *testing.T, authCalls *int, timeout time.Duration) http.Handler {
	t.Helper()
	handler, err := NewRouter(RouterConfig{
		Organizations: routerOrganizations{}, Members: routerMembers{}, Rounds: routerRounds{}, Pairs: routerPairs{},
		Authenticate: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				(*authCalls)++
				if request.Header.Get("Authorization") != "Bearer valid" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, request)
			})
		},
		CORS: NewCORS([]string{"https://allowed.example"}), StaticDir: filepath.Join("..", "..", "static"),
		RequestTimeout: timeout, MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestRouterEveryAPIRouteAndAllowedMethod(t *testing.T) {
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	part, err := writer.CreateFormFile("members", "ignored.csv")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, "name,email\nA,a@example.com\n")
	_ = writer.Close()

	tests := []struct {
		method, target, body, contentType string
		status                            int
	}{
		{http.MethodGet, "/members?org=o", "", "", http.StatusOK},
		{http.MethodPost, "/members?org=o", multipartBody.String(), writer.FormDataContentType(), http.StatusCreated},
		{http.MethodGet, "/orgs?admin=a", "", "", http.StatusOK},
		{http.MethodPost, "/org?admin=a", `{"org":"o"}`, "application/json", http.StatusCreated},
		{http.MethodPost, "/crossmatchtrait?org=o", `{"trait":"team"}`, "application/json", http.StatusCreated},
		{http.MethodGet, "/rounds?org=o", "", "", http.StatusOK},
		{http.MethodPost, "/round?org=o&round=2026-01-02T03:04:05Z", "", "", http.StatusCreated},
		{http.MethodDelete, "/round?org=o&roundId=0", "", "", http.StatusCreated},
		{http.MethodGet, "/pairs?org=o", "", "", http.StatusOK},
	}
	authCalls := 0
	handler := newTestRouter(t, &authCalls, time.Second)
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer valid")
		if test.contentType != "" {
			request.Header.Set("Content-Type", test.contentType)
		}
		handler.ServeHTTP(recorder, request)
		if recorder.Code != test.status {
			t.Errorf("%s %s = %d: %s", test.method, test.target, recorder.Code, recorder.Body.String())
		}
	}
	if authCalls != len(tests) {
		t.Fatalf("auth calls = %d, want %d", authCalls, len(tests))
	}
}

func TestRouterMiddlewareOrderStaticAndPrecedence(t *testing.T) {
	authCalls := 0
	handler := newTestRouter(t, &authCalls, 20*time.Millisecond)

	preflight := httptest.NewRequest(http.MethodOptions, "/members", nil)
	preflight.Header.Set("Origin", "https://allowed.example")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflightResult := httptest.NewRecorder()
	handler.ServeHTTP(preflightResult, preflight)
	if preflightResult.Code != http.StatusNoContent || authCalls != 0 {
		t.Fatalf("preflight = %d, auth calls %d", preflightResult.Code, authCalls)
	}

	forbidden := httptest.NewRequest(http.MethodGet, "/members?org=o", nil)
	forbidden.Header.Set("Origin", "https://forbidden.example")
	forbiddenResult := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenResult, forbidden)
	if forbiddenResult.Code != http.StatusForbidden || authCalls != 0 {
		t.Fatalf("forbidden = %d, auth calls %d", forbiddenResult.Code, authCalls)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/orgs?admin=a", nil))
	if unauthorized.Code != http.StatusUnauthorized || authCalls != 1 {
		t.Fatalf("unauthorized = %d, auth calls %d", unauthorized.Code, authCalls)
	}

	for _, asset := range []string{"privacy.html", "sample.csv"} {
		expected, err := os.ReadFile(filepath.Join("..", "..", "static", asset))
		if err != nil {
			t.Fatal(err)
		}
		for _, origin := range []string{"", "https://forbidden.example"} {
			request := httptest.NewRequest(http.MethodGet, "/"+asset, nil)
			if origin != "" {
				request.Header.Set("Origin", origin)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), expected) {
				t.Errorf("static %s origin %q = %d, exact=%v", asset, origin, recorder.Code, bytes.Equal(recorder.Body.Bytes(), expected))
			}
		}
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/does-not-exist", nil))
	if missing.Code != http.StatusNotFound {
		t.Errorf("missing static = %d", missing.Code)
	}
	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/org", nil))
	if api.Code != http.StatusUnauthorized {
		t.Errorf("API route was shadowed by static: %d", api.Code)
	}
}

func TestRouterConstructionValidation(t *testing.T) {
	_, err := NewRouter(RouterConfig{})
	if err == nil {
		t.Fatal("NewRouter accepted missing dependencies")
	}
	config := RouterConfig{Organizations: routerOrganizations{}, Members: routerMembers{}, Rounds: routerRounds{}, Pairs: routerPairs{}, Authenticate: func(h http.Handler) http.Handler { return h }, StaticDir: "missing", RequestTimeout: time.Second}
	if _, err := NewRouter(config); err == nil {
		t.Fatal("NewRouter accepted missing static directory")
	}
}

func TestRouterAppliesRequestTimeout(t *testing.T) {
	handler, err := NewRouter(RouterConfig{
		Organizations: blockingOrganizations{}, Members: routerMembers{}, Rounds: routerRounds{}, Pairs: routerPairs{},
		Authenticate: func(handler http.Handler) http.Handler { return handler }, CORS: NewCORS(nil),
		StaticDir: filepath.Join("..", "..", "static"), RequestTimeout: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/orgs?admin=a", nil))
	if recorder.Code != http.StatusInternalServerError || time.Since(start) > time.Second {
		t.Fatalf("timed request = %d after %v", recorder.Code, time.Since(start))
	}
}
