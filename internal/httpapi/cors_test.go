package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	allowedOrigin     = "https://app.example"
	corsForbiddenBody = "{\"error\":\"origin not allowed\"}\n"
)

func TestCORSAllowedOriginDelegatesAndSetsContract(t *testing.T) {
	called := false
	handler := NewCORS([]string{allowedOrigin}).Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", allowedOrigin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if !called || response.Code != http.StatusAccepted {
		t.Fatalf("called=%t status=%d", called, response.Code)
	}
	assertCORSHeaders(t, response.Header(), allowedOrigin)
}

func TestCORSNoOriginDelegatesWithoutAllowOrigin(t *testing.T) {
	called := false
	handler := NewCORS([]string{allowedOrigin}).Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	if !called || response.Code != http.StatusCreated {
		t.Fatalf("called=%t status=%d", called, response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	assertVaryOrigin(t, response.Header())
}

func TestCORSRejectsDisallowedOrigin(t *testing.T) {
	handler := NewCORS([]string{allowedOrigin}).Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler called")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if response.Body.String() != corsForbiddenBody {
		t.Fatalf("body = %q", response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	assertVaryOrigin(t, response.Header())
}

func TestCORSPreflightShortCircuits(t *testing.T) {
	handler := NewCORS([]string{allowedOrigin}).Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler called")
	}))
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", allowedOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, TOKEN")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	assertCORSHeaders(t, response.Header(), allowedOrigin)
}

func TestCORSPreflightRejectsUnsupportedRequest(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		headers string
	}{
		{name: "method", method: http.MethodPut},
		{name: "header", method: http.MethodGet, headers: "X-Secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewCORS([]string{allowedOrigin}).Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("next handler called")
			}))
			req := httptest.NewRequest(http.MethodOptions, "/", nil)
			req.Header.Set("Origin", allowedOrigin)
			req.Header.Set("Access-Control-Request-Method", test.method)
			if test.headers != "" {
				req.Header.Set("Access-Control-Request-Headers", test.headers)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusForbidden || response.Body.String() != corsForbiddenBody {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			assertVaryOrigin(t, response.Header())
		})
	}
}

func TestCORSNonPreflightOptionsDelegates(t *testing.T) {
	called := false
	handler := NewCORS([]string{allowedOrigin}).Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", allowedOrigin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if !called || response.Code != http.StatusOK {
		t.Fatalf("called=%t status=%d", called, response.Code)
	}
}

func TestCORSExactAllowlistDoesNotReflectVariants(t *testing.T) {
	handler := NewCORS([]string{"  " + allowedOrigin + "  ", ""}).Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, origin := range []string{allowedOrigin + "/", strings.ToUpper(allowedOrigin), "null"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Fatalf("origin %q status = %d", origin, response.Code)
		}
	}
}

func TestCORSVaryPreservesExistingValuesWithoutDuplication(t *testing.T) {
	handler := NewCORS([]string{allowedOrigin}).Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	joined := strings.Join(response.Header().Values("Vary"), ",")
	if !strings.Contains(joined, "Origin") || !strings.Contains(joined, "Accept-Encoding") {
		t.Fatalf("Vary = %q", joined)
	}

	header := http.Header{"Vary": []string{"Accept-Encoding, Origin"}}
	addVaryOrigin(header)
	if got := strings.Count(strings.Join(header.Values("Vary"), ","), "Origin"); got != 1 {
		t.Fatalf("Origin count = %d", got)
	}
}

func TestCORSNilHandlerIsSafe(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	NewCORS(nil).Handler(nil).ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	assertVaryOrigin(t, response.Header())
}

func assertCORSHeaders(t *testing.T, header http.Header, origin string) {
	t.Helper()
	if got := header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := header.Get("Access-Control-Allow-Methods"); got != allowMethods {
		t.Fatalf("Access-Control-Allow-Methods = %q", got)
	}
	if got := header.Get("Access-Control-Allow-Headers"); got != allowHeaders {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
	assertVaryOrigin(t, header)
}

func assertVaryOrigin(t *testing.T, header http.Header) {
	t.Helper()
	for _, value := range header.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "Origin") {
				return
			}
		}
	}
	t.Fatalf("Vary = %q, want Origin", header.Values("Vary"))
}
