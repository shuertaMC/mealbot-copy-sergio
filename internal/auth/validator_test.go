package auth

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://issuer.example/"
	testAudience = "https://api.example/"
)

func TestValidatorAcceptsValidRS256TokenFromJWKS(t *testing.T) {
	key := generateTestKey(t)
	body := marshalJWKS(t, rsaJWK("current-key", &key.PublicKey))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	provider, err := NewJWKSProvider(JWKSConfig{URL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	validator, err := NewValidator(ValidatorConfig{
		Issuer: testIssuer, Audience: testAudience, Keys: provider, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token := signTestToken(t, jwt.SigningMethodRS256, key, "current-key", jwt.MapClaims{
		"iss": testIssuer,
		"aud": []string{"another-audience", testAudience},
		"exp": now.Add(time.Minute).Unix(),
		"nbf": now.Add(-time.Minute).Unix(),
		"iat": now.Add(-time.Minute).Unix(),
	})
	if err := validator.Validate(context.Background(), token); err != nil {
		t.Fatalf("Validate(valid token) = %v", err)
	}
}

func TestValidatorRejectsInvalidClaimsAlgorithmsSignaturesAndKeyIDs(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	trusted := generateTestKey(t)
	untrusted := generateTestKey(t)
	validClaims := func() jwt.MapClaims {
		return jwt.MapClaims{
			"iss": testIssuer,
			"aud": testAudience,
			"exp": now.Add(time.Minute).Unix(),
			"nbf": now.Add(-time.Minute).Unix(),
		}
	}

	tests := []struct {
		name   string
		method jwt.SigningMethod
		key    any
		kid    any
		claims func() jwt.MapClaims
		raw    string
	}{
		{
			name: "wrong issuer", method: jwt.SigningMethodRS256, key: trusted, kid: "trusted",
			claims: func() jwt.MapClaims {
				claims := validClaims()
				claims["iss"] = "https://other.example/"
				return claims
			},
		},
		{
			name: "missing issuer", method: jwt.SigningMethodRS256, key: trusted, kid: "trusted",
			claims: func() jwt.MapClaims {
				claims := validClaims()
				delete(claims, "iss")
				return claims
			},
		},
		{
			name: "wrong audience", method: jwt.SigningMethodRS256, key: trusted, kid: "trusted",
			claims: func() jwt.MapClaims {
				claims := validClaims()
				claims["aud"] = "other"
				return claims
			},
		},
		{
			name: "missing audience", method: jwt.SigningMethodRS256, key: trusted, kid: "trusted",
			claims: func() jwt.MapClaims {
				claims := validClaims()
				delete(claims, "aud")
				return claims
			},
		},
		{
			name: "expired", method: jwt.SigningMethodRS256, key: trusted, kid: "trusted",
			claims: func() jwt.MapClaims {
				claims := validClaims()
				claims["exp"] = now.Add(-time.Second).Unix()
				return claims
			},
		},
		{
			name: "not active", method: jwt.SigningMethodRS256, key: trusted, kid: "trusted",
			claims: func() jwt.MapClaims {
				claims := validClaims()
				claims["nbf"] = now.Add(time.Second).Unix()
				return claims
			},
		},
		{name: "wrong signature", method: jwt.SigningMethodRS256, key: untrusted, kid: "trusted", claims: validClaims},
		{name: "missing key id", method: jwt.SigningMethodRS256, key: trusted, claims: validClaims},
		{name: "empty key id", method: jwt.SigningMethodRS256, key: trusted, kid: " ", claims: validClaims},
		{name: "non-string key id", method: jwt.SigningMethodRS256, key: trusted, kid: 42, claims: validClaims},
		{name: "RS384", method: jwt.SigningMethodRS384, key: trusted, kid: "trusted", claims: validClaims},
		{name: "HS256", method: jwt.SigningMethodHS256, key: []byte("not-an-rsa-key"), kid: "trusted", claims: validClaims},
		{name: "malformed", raw: "not.a.jwt"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &recordingKeyProvider{keys: map[string]*rsa.PublicKey{"trusted": &trusted.PublicKey}}
			validator, err := NewValidator(ValidatorConfig{
				Issuer: testIssuer, Audience: testAudience, Keys: provider, Now: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			raw := test.raw
			if raw == "" {
				raw = signTestToken(t, test.method, test.key, test.kid, test.claims())
			}
			if err := validator.Validate(context.Background(), raw); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("Validate(invalid token) = %v, want ErrUnauthorized", err)
			}
		})
	}
}

func TestValidatorRejectsNoneAlgorithm(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	provider := &recordingKeyProvider{}
	validator, err := NewValidator(ValidatorConfig{
		Issuer: testIssuer, Audience: testAudience, Keys: provider, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	token := signTestToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, "trusted", jwt.MapClaims{
		"iss": testIssuer, "aud": testAudience, "exp": now.Add(time.Minute).Unix(),
	})
	if err := validator.Validate(context.Background(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Validate(none token) = %v, want ErrUnauthorized", err)
	}
	if provider.Calls() != 0 {
		t.Fatalf("none token caused %d key lookups, want 0", provider.Calls())
	}
}

func TestValidatorUsesJWTDefaultTemporalSemantics(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	key := generateTestKey(t)
	provider := &recordingKeyProvider{keys: map[string]*rsa.PublicKey{"trusted": &key.PublicKey}}
	validator, err := NewValidator(ValidatorConfig{
		Issuer: testIssuer, Audience: testAudience, Keys: provider, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	// jwt/v5 validates exp and nbf when present, but does not require exp or
	// reject a future iat unless WithIssuedAt is explicitly selected.
	token := signTestToken(t, jwt.SigningMethodRS256, key, "trusted", jwt.MapClaims{
		"iss": testIssuer, "aud": testAudience, "iat": now.Add(time.Hour).Unix(),
	})
	if err := validator.Validate(context.Background(), token); err != nil {
		t.Fatalf("Validate(jwt/v5 default temporal claims) = %v", err)
	}
}

type recordingKeyProvider struct {
	mu    sync.Mutex
	keys  map[string]*rsa.PublicKey
	err   error
	calls int
}

func (p *recordingKeyProvider) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return p.keys[kid], nil
}

func (p *recordingKeyProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}
