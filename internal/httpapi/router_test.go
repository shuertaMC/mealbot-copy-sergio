package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/johnamadeo/mealbot/internal/auth"
)

func namedHandler(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, name)
	})
}

func testHandlers() Handlers {
	return Handlers{
		Members:         namedHandler("members"),
		Organizations:   namedHandler("orgs"),
		Organization:    namedHandler("org"),
		CrossMatchTrait: namedHandler("crossmatchtrait"),
		Rounds:          namedHandler("rounds"),
		Round:           namedHandler("round"),
		Pairs:           namedHandler("pairs"),
	}
}

func TestMiddlewareCompositionOrder(t *testing.T) {
	events := make([]string, 0)
	first := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			events = append(events, "first-before")
			next.ServeHTTP(w, r)
			events = append(events, "first-after")
		})
	}
	second := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			events = append(events, "second-before")
			next.ServeHTTP(w, r)
			events = append(events, "second-after")
		})
	}
	middleware := Middleware{MiddlewareHandlers: []func(http.Handler) http.Handler{first, second}}
	handler := middleware.Apply(func(http.ResponseWriter, *http.Request) {
		events = append(events, "core")
	})
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	want := []string{"second-before", "first-before", "core", "first-after", "second-after"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRouterRegistersSevenRoutesWithCorsOutsideAuth(t *testing.T) {
	authCalls := 0
	authMiddleware := auth.MiddlewareFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authCalls++
			next.ServeHTTP(w, r)
		})
	})
	router := NewRouter(RouterConfig{
		Handlers:        testHandlers(),
		Auth:            authMiddleware,
		CORS:            CompatibilityCORS(),
		StaticDirectory: "../../static",
	})

	tests := []struct{ path, body string }{
		{"/members", "members"},
		{"/orgs", "orgs"},
		{"/org", "org"},
		{"/crossmatchtrait", "crossmatchtrait"},
		{"/rounds", "rounds"},
		{"/round", "round"},
		{"/pairs", "pairs"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("GET", test.path, nil)
		req.Header.Set("Origin", "https://client.example")
		router.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK || recorder.Body.String() != test.body {
			t.Fatalf("GET %s = %d %q", test.path, recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://client.example" {
			t.Fatalf("GET %s allow origin = %q", test.path, got)
		}
	}
	if authCalls != len(tests) {
		t.Fatalf("auth calls = %d, want %d", authCalls, len(tests))
	}

	for _, test := range tests {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("OPTIONS", test.path, nil)
		req.Header.Set("Origin", "https://client.example")
		router.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
			t.Fatalf("OPTIONS %s = %d %q", test.path, recorder.Code, recorder.Body.String())
		}
		if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != AllowedHeaders {
			t.Fatalf("allow headers = %q", got)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != AllowedMethods {
			t.Fatalf("allow methods = %q", got)
		}
	}
	if authCalls != len(tests) {
		t.Fatalf("OPTIONS reached auth: calls = %d, want %d", authCalls, len(tests))
	}
}

func TestRouterStaticAssetsArePublicAndReal(t *testing.T) {
	authCalls := 0
	router := NewRouter(RouterConfig{
		Handlers: testHandlers(),
		Auth: auth.MiddlewareFunc(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				authCalls++
				next.ServeHTTP(w, r)
			})
		}),
		CORS:            CompatibilityCORS(),
		StaticDirectory: "../../static",
	})
	for _, name := range []string{"privacy.html", "sample.csv"} {
		want, err := os.ReadFile("../../static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("GET", "/"+name, nil))
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), want) {
			t.Fatalf("static %s = status %d, body matches %v", name, recorder.Code, bytes.Equal(recorder.Body.Bytes(), want))
		}
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("static %s received API CORS header %q", name, got)
		}
	}
	if authCalls != 0 {
		t.Fatalf("static requests reached auth %d times", authCalls)
	}
}
