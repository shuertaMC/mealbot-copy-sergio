package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://issuer.example/"
	testAudience = "https://api.example/"
	testKID      = "test-key"
)

type recordingKeySource struct {
	mu    sync.Mutex
	key   *rsa.PublicKey
	err   error
	calls int
}

func (s *recordingKeySource) Key(_ context.Context, _ string) (*rsa.PublicKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.key, s.err
}

func (s *recordingKeySource) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestNewValidatorRejectsInvalidConfiguration(t *testing.T) {
	key := mustRSAKey(t)
	source := &recordingKeySource{key: &key.PublicKey}
	tests := []struct {
		name     string
		issuer   string
		audience string
		keys     KeySource
	}{
		{name: "missing issuer", audience: testAudience, keys: source},
		{name: "missing audience", issuer: testIssuer, keys: source},
		{name: "missing source", issuer: testIssuer, audience: testAudience},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewValidator(test.issuer, test.audience, test.keys); err == nil {
				t.Fatal("NewValidator() error = nil")
			}
		})
	}
}

func TestValidatorClaimsAndKeyIdentifier(t *testing.T) {
	privateKey := mustRSAKey(t)
	tests := []struct {
		name   string
		claims jwt.MapClaims
		kid    any
		wantOK bool
	}{
		{name: "string audience", claims: validClaims(testAudience), kid: testKID, wantOK: true},
		{name: "array audience", claims: validClaims([]string{"another", testAudience}), kid: testKID, wantOK: true},
		{name: "wrong issuer", claims: jwt.MapClaims{"iss": "https://wrong.example/", "aud": testAudience}, kid: testKID},
		{name: "wrong audience", claims: validClaims("not-the-api"), kid: testKID},
		{name: "array missing audience", claims: validClaims([]string{"another"}), kid: testKID},
		{name: "missing audience", claims: jwt.MapClaims{"iss": testIssuer}, kid: testKID},
		{name: "missing kid", claims: validClaims(testAudience)},
		{name: "non-string kid", claims: validClaims(testAudience), kid: 7},
		{name: "blank kid", claims: validClaims(testAudience), kid: ""},
		{name: "padded kid", claims: validClaims(testAudience), kid: " test-key"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &recordingKeySource{key: &privateKey.PublicKey}
			validator, err := NewValidator(testIssuer, testAudience, source)
			if err != nil {
				t.Fatalf("NewValidator() error = %v", err)
			}
			token := mustSignToken(t, jwt.SigningMethodRS256, privateKey, test.claims, test.kid)
			err = validator.Validate(context.Background(), token)
			if test.wantOK && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if !test.wantOK && !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("Validate() error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestValidatorRejectsAlgorithmBeforeKeyLookup(t *testing.T) {
	privateKey := mustRSAKey(t)
	source := &recordingKeySource{key: &privateKey.PublicKey}
	validator, err := NewValidator(testIssuer, testAudience, source)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	token := mustSignToken(t, jwt.SigningMethodHS256, []byte("secret"), validClaims(testAudience), testKID)
	if err := validator.Validate(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Validate() error = %v, want ErrInvalidToken", err)
	}
	if calls := source.callCount(); calls != 0 {
		t.Fatalf("key source calls = %d, want 0", calls)
	}
}

func TestValidatorNormalizesParsingAndKeyErrors(t *testing.T) {
	privateKey := mustRSAKey(t)
	source := &recordingKeySource{err: errors.New("provider detail")}
	validator, err := NewValidator(testIssuer, testAudience, source)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	inputs := []string{
		"not-a-token",
		mustSignToken(t, jwt.SigningMethodRS256, privateKey, validClaims(testAudience), testKID),
	}
	for _, input := range inputs {
		if err := validator.Validate(context.Background(), input); err != ErrInvalidToken {
			t.Fatalf("Validate(%q) error = %v, want exact ErrInvalidToken", input, err)
		}
	}
}

type validatorFunc func(context.Context, string) error

func (fn validatorFunc) Validate(ctx context.Context, token string) error { return fn(ctx, token) }

func TestMiddlewareAuthorizationContract(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		valid   bool
	}{
		{name: "missing"},
		{name: "empty", headers: []string{""}},
		{name: "wrong scheme", headers: []string{"Basic abc"}},
		{name: "lowercase scheme", headers: []string{"bearer abc"}, valid: true},
		{name: "mixed-case scheme", headers: []string{"bEaReR abc"}, valid: true},
		{name: "extra space", headers: []string{"Bearer  abc"}},
		{name: "missing token", headers: []string{"Bearer "}},
		{name: "token whitespace", headers: []string{"Bearer abc def"}},
		{name: "duplicate", headers: []string{"Bearer abc", "Bearer abc"}},
		{name: "valid", headers: []string{"Bearer abc"}, valid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			middleware := NewMiddleware(validatorFunc(func(_ context.Context, token string) error {
				if token != "abc" {
					t.Fatalf("token = %q, want abc", token)
				}
				return nil
			}))
			handler := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusAccepted)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, value := range test.headers {
				req.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if test.valid {
				if response.Code != http.StatusAccepted || !called {
					t.Fatalf("response = %d, called = %t", response.Code, called)
				}
				return
			}
			if response.Code != http.StatusUnauthorized || called {
				t.Fatalf("response = %d, called = %t", response.Code, called)
			}
			if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q", got)
			}
			if got := response.Body.String(); got != invalidTokenResponse {
				t.Fatalf("body = %q", got)
			}
		})
	}
}

func TestMiddlewareInvalidTokenAndNilValidator(t *testing.T) {
	for _, middleware := range []Middleware{
		NewMiddleware(nil),
		NewMiddleware(validatorFunc(func(context.Context, string) error { return errors.New("sensitive") })),
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer abc")
		response := httptest.NewRecorder()
		middleware.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("next handler called")
		})).ServeHTTP(response, req)
		if response.Code != http.StatusUnauthorized || response.Body.String() != invalidTokenResponse {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
	}
}

func TestMiddlewareOptionsBypassesAuthentication(t *testing.T) {
	validatorCalled := false
	nextCalled := false
	middleware := NewMiddleware(validatorFunc(func(context.Context, string) error {
		validatorCalled = true
		return errors.New("should not be called")
	}))
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	response := httptest.NewRecorder()
	middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, req)
	if validatorCalled || !nextCalled || response.Code != http.StatusNoContent {
		t.Fatalf("validatorCalled=%t nextCalled=%t status=%d", validatorCalled, nextCalled, response.Code)
	}
}

func validClaims(audience any) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": testIssuer,
		"aud": audience,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
}

func mustSignToken(t *testing.T, method jwt.SigningMethod, key any, claims jwt.Claims, kid any) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	if kid != nil {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return signed
}

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	return key
}

func TestMiddlewareRejectsTokenWithControlWhitespace(t *testing.T) {
	middleware := NewMiddleware(validatorFunc(func(context.Context, string) error {
		t.Fatal("validator called")
		return nil
	}))
	for _, token := range []string{"abc\tdef", "abc\ndef", "abc\rdef"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header["Authorization"] = []string{"Bearer " + token}
		response := httptest.NewRecorder()
		middleware.Handler(nil).ServeHTTP(response, req)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("token %q status = %d", strings.ReplaceAll(token, "\n", "\\n"), response.Code)
		}
	}
}
