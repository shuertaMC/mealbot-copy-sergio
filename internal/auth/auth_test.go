package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBearerTokenValidatesPresenceAndFormat(t *testing.T) {
	for _, header := range []string{"", "Basic token", "Bearer", "Bearer one two", "token"} {
		t.Run(header, func(t *testing.T) {
			if _, err := BearerToken(header); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("BearerToken(%q) = %v, want ErrUnauthorized", header, err)
			}
		})
	}
	for _, header := range []string{"Bearer token", "bearer token", "  BEARER   token  "} {
		t.Run(header, func(t *testing.T) {
			got, err := BearerToken(header)
			if err != nil || got != "token" {
				t.Fatalf("BearerToken(%q) = %q, %v", header, got, err)
			}
		})
	}
}

func TestHTTPMiddlewareReturnsGenericUnauthorizedAndBypassesOptions(t *testing.T) {
	secretToken := "token-that-must-not-leak"
	validator := tokenValidatorFunc(func(context.Context, string) error {
		return errors.New("provider-secret")
	})
	called := false
	handler := NewMiddleware(validator).Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/members", nil)
	request.Header.Set("Authorization", "Bearer "+secretToken)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if called {
		t.Fatal("unauthorized request reached protected handler")
	}
	if got := recorder.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q, want Bearer", got)
	}
	for _, leaked := range []string{secretToken, "provider-secret"} {
		if strings.Contains(recorder.Body.String(), leaked) {
			t.Fatalf("unauthorized response leaked %q: %q", leaked, recorder.Body.String())
		}
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodOptions, "/members", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !called {
		t.Fatalf("OPTIONS status = %d, called = %v", recorder.Code, called)
	}
}

type tokenValidatorFunc func(context.Context, string) error

func (f tokenValidatorFunc) Validate(ctx context.Context, token string) error {
	return f(ctx, token)
}
