package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://issuer.example.test/"
	testAudience = "https://api.example.test/"
	testKID      = "test-key"
)

type fixture struct {
	key     *rsa.PrivateKey
	jwks    *httptest.Server
	handler http.Handler
	reached *int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	jwksJSON, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": testKID,
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(jwksJSON)
	}))
	t.Cleanup(jwks.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mw := NewMiddleware(ctx, Config{Issuer: testIssuer, Audience: testAudience, JWKSURL: jwks.URL})

	reached := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.WriteHeader(http.StatusOK)
	})

	return &fixture{key: key, jwks: jwks, handler: mw.Handler(next), reached: &reached}
}

func (f *fixture) sign(t *testing.T, method jwt.SigningMethod, key any, iss string, aud []string, exp time.Time) string {
	t.Helper()
	claims := jwt.RegisteredClaims{
		Issuer:    iss,
		Audience:  jwt.ClaimStrings(aud),
		ExpiresAt: jwt.NewNumericDate(exp),
		IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
	}
	tok := jwt.NewWithClaims(method, claims)
	tok.Header["kid"] = testKID
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fixture) do(method, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/members?org=x", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func TestValidTokenPasses(t *testing.T) {
	f := newFixture(t)
	tok := f.sign(t, jwt.SigningMethodRS256, f.key, testIssuer, []string{testAudience, "other"}, time.Now().Add(time.Hour))

	rec := f.do(http.MethodGet, "Bearer "+tok)
	if rec.Code != http.StatusOK || *f.reached != 1 {
		t.Fatalf("expected request to pass, got %d (reached=%d) body=%s", rec.Code, *f.reached, rec.Body.String())
	}
}

func TestOptionsPreflightPassesWithoutToken(t *testing.T) {
	f := newFixture(t)
	rec := f.do(http.MethodOptions, "")
	if rec.Code != http.StatusOK || *f.reached != 1 {
		t.Fatalf("preflight should pass through, got %d", rec.Code)
	}
}

func TestInvalidRequestsGet401(t *testing.T) {
	f := newFixture(t)
	hmacTok := f.sign(t, jwt.SigningMethodHS256, []byte("shared-secret"), testIssuer, []string{testAudience}, time.Now().Add(time.Hour))
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"no header":        "",
		"malformed header": "Token abc",
		"garbage token":    "Bearer not-a-jwt",
		"bad alg (HS256)":  "Bearer " + hmacTok,
		"wrong issuer":     "Bearer " + f.sign(t, jwt.SigningMethodRS256, f.key, "https://evil.example.test/", []string{testAudience}, time.Now().Add(time.Hour)),
		"wrong audience":   "Bearer " + f.sign(t, jwt.SigningMethodRS256, f.key, testIssuer, []string{"https://other.example.test/"}, time.Now().Add(time.Hour)),
		"expired":          "Bearer " + f.sign(t, jwt.SigningMethodRS256, f.key, testIssuer, []string{testAudience}, time.Now().Add(-time.Hour)),
		"unknown signer":   "Bearer " + f.sign(t, jwt.SigningMethodRS256, otherKey, testIssuer, []string{testAudience}, time.Now().Add(time.Hour)),
	}

	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			rec := f.do(http.MethodGet, header)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rec.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["Message"] == "" {
				t.Fatalf("expected JSON error body, got %q (%v)", rec.Body.String(), err)
			}
		})
	}
	if *f.reached != 0 {
		t.Fatalf("protected handler reached %d times", *f.reached)
	}
}

func TestUnreachableJWKSGives401(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more

	mw := NewMiddleware(ctx, Config{Issuer: testIssuer, Audience: testAudience, JWKSURL: url})
	h := mw.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be reached")
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer abc.def.ghi")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
