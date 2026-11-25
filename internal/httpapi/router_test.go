package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
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

type eventWriter struct {
	events *[]string
}

func (w eventWriter) Write(p []byte) (int, error) {
	*w.events = append(*w.events, "request-log")
	return len(p), nil
}

func TestMiddlewareCompositionOrder(t *testing.T) {
	events := make([]string, 0)
	authMiddleware := auth.MiddlewareFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://client.example" {
				t.Fatalf("CORS header before auth = %q", got)
			}
			events = append(events, "auth-before")
			next.ServeHTTP(w, r)
			events = append(events, "auth-after")
		})
	})
	handlers := testHandlers()
	handlers.Members = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		events = append(events, "route")
	})
	router := NewRouter(RouterConfig{
		Handlers:        handlers,
		Auth:            authMiddleware,
		CORS:            CompatibilityCORS(),
		Logger:          slog.New(slog.NewJSONHandler(eventWriter{events: &events}, nil)),
		StaticDirectory: "../../static",
	})
	request := httptest.NewRequest(http.MethodGet, "/members", nil)
	request.Header.Set("Origin", "https://client.example")
	router.ServeHTTP(httptest.NewRecorder(), request)
	want := []string{"auth-before", "route", "auth-after", "request-log"}
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
	files := []struct {
		name        string
		contentType string
		sha256      string
	}{
		{"privacy.html", "text/html; charset=utf-8", "8002d3f7aa546aac4f582314d8c088d53649c0b6164f2c5b993dc222c315a17c"},
		{"sample.csv", "text/csv; charset=utf-8", "bc8652405a29f77ae729aa6da49f2603326b1d94d2bed60117c99cfcb32e9cd1"},
	}
	for _, file := range files {
		want, err := os.ReadFile("../../static/" + file.name)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(want)
		if got := hex.EncodeToString(digest[:]); got != file.sha256 {
			t.Fatalf("tracked %s digest = %s, want %s", file.name, got, file.sha256)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("GET", "/"+file.name, nil))
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), want) {
			t.Fatalf("static %s = status %d, body matches %v", file.name, recorder.Code, bytes.Equal(recorder.Body.Bytes(), want))
		}
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("static %s received API CORS header %q", file.name, got)
		}
		if got := recorder.Header().Get("Content-Type"); got != file.contentType {
			t.Fatalf("static %s Content-Type = %q, want %q", file.name, got, file.contentType)
		}
		if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(len(want)) {
			t.Fatalf("static %s Content-Length = %q, want %d", file.name, got, len(want))
		}
	}
	if authCalls != 0 {
		t.Fatalf("static requests reached auth %d times", authCalls)
	}
}
