package auth

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

func TestHTTPMiddlewarePreservesLegacyRejectionAndValidPassThroughContracts(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	trusted := generateTestKey(t)
	untrusted := generateTestKey(t)
	validator, err := NewValidator(ValidatorConfig{
		Issuer:   testIssuer,
		Audience: testAudience,
		Keys: keyProviderFunc(func(_ context.Context, kid string) (*rsa.PublicKey, error) {
			if kid != "trusted" {
				return nil, ErrUnauthorized
			}
			return &trusted.PublicKey, nil
		}),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{
		"iss": testIssuer,
		"aud": testAudience,
		"exp": now.Add(time.Minute).Unix(),
		"nbf": now.Add(-time.Minute).Unix(),
	}
	invalidSignature := signTestToken(t, jwt.SigningMethodRS256, untrusted, "trusted", claims)
	valid := signTestToken(t, jwt.SigningMethodRS256, trusted, "trusted", claims)

	tests := []struct {
		name       string
		header     string
		wantCalled bool
	}{
		{name: "missing credential"},
		{name: "malformed scheme", header: "Basic token"},
		{name: "malformed bearer", header: "Bearer"},
		{name: "invalid signature", header: "Bearer " + invalidSignature},
		{name: "valid token", header: "Bearer " + valid, wantCalled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			handler := NewMiddleware(validator).Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.Header().Set("X-Protected-Handler", "called")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte("accepted"))
			}))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/members", nil)
			request.Header.Set("Authorization", test.header)
			handler.ServeHTTP(recorder, request)

			if called != test.wantCalled {
				t.Fatalf("protected handler called = %t, want %t", called, test.wantCalled)
			}
			if test.wantCalled {
				if recorder.Code != http.StatusAccepted || recorder.Body.String() != "accepted" || recorder.Header().Get("X-Protected-Handler") != "called" {
					t.Fatalf("valid response = %d %q headers=%v", recorder.Code, recorder.Body.String(), recorder.Header())
				}
				return
			}
			if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
				t.Fatalf("rejection = %d %q, want empty 200", recorder.Code, recorder.Body.String())
			}
			if len(recorder.Header()) != 0 {
				t.Fatalf("rejection invented headers: %v", recorder.Header())
			}
		})
	}
}

func TestHTTPMiddlewareBypassesOptions(t *testing.T) {
	called := false
	handler := NewMiddleware(tokenValidatorFunc(func(context.Context, string) error {
		return ErrUnauthorized
	})).Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/members", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !called {
		t.Fatalf("OPTIONS status = %d, called = %v", recorder.Code, called)
	}
}

type tokenValidatorFunc func(context.Context, string) error

func (f tokenValidatorFunc) Validate(ctx context.Context, token string) error {
	return f(ctx, token)
}

type keyProviderFunc func(context.Context, string) (*rsa.PublicKey, error)

func (f keyProviderFunc) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	return f(ctx, kid)
}
