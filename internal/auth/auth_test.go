package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/logging"
)

const (
	testIssuer   = "https://test.auth0.example/"
	testAudience = "https://mealbot.example/api"
	testKID      = "key-1"
)

type fixture struct {
	key       *rsa.PrivateKey
	jwks      *httptest.Server
	fetches   *atomic.Int32
	validator *Validator
}

func mustKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func rawKey(key *rsa.PrivateKey, kid string) JSONWebKey {
	return JSONWebKey{
		Kty: "RSA",
		Kid: kid,
		Use: "sig",
		N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

func certKey(t *testing.T, key *rsa.PrivateKey, kid string) JSONWebKey {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mealbot-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return JSONWebKey{Kty: "RSA", Kid: kid, Use: "sig", X5c: []string{base64.StdEncoding.EncodeToString(der)}}
}

func newFixture(t *testing.T, jwk func(key *rsa.PrivateKey) JSONWebKey) *fixture {
	t.Helper()
	key := mustKey(t)
	fetches := &atomic.Int32{}
	set := JSONWebKeySet{Keys: []JSONWebKey{jwk(key)}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(srv.Close)

	cfg := config.Auth0{Issuer: testIssuer, Audience: testAudience, JWKSURL: srv.URL}
	return &fixture{
		key:       key,
		jwks:      srv,
		fetches:   fetches,
		validator: NewValidator(cfg, NewJWKSClient(srv.URL, nil), logging.Discard()),
	}
}

func (f *fixture) sign(t *testing.T, method jwt.SigningMethod, key interface{}, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss": testIssuer,
		"aud": []string{testAudience, "https://test.auth0.example/userinfo"},
		"sub": "auth0|123",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
}

func TestValidateAcceptsValidToken(t *testing.T) {
	for name, jwk := range map[string]func(*rsa.PrivateKey) JSONWebKey{
		"n/e": func(k *rsa.PrivateKey) JSONWebKey { return rawKey(k, testKID) },
		"x5c": func(k *rsa.PrivateKey) JSONWebKey { return certKey(t, k, testKID) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, jwk)
			tok := f.sign(t, jwt.SigningMethodRS256, f.key, testKID, validClaims())
			if _, err := f.validator.Validate(t.Context(), tok); err != nil {
				t.Fatalf("expected valid token, got %v", err)
			}
		})
	}
}

func TestValidateAcceptsStringAudience(t *testing.T) {
	f := newFixture(t, func(k *rsa.PrivateKey) JSONWebKey { return rawKey(k, testKID) })
	claims := validClaims()
	claims["aud"] = testAudience
	tok := f.sign(t, jwt.SigningMethodRS256, f.key, testKID, claims)
	if _, err := f.validator.Validate(t.Context(), tok); err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
}

func TestValidateRejections(t *testing.T) {
	f := newFixture(t, func(k *rsa.PrivateKey) JSONWebKey { return rawKey(k, testKID) })
	otherKey := mustKey(t)

	tests := []struct {
		name    string
		token   func() string
		wantErr error
	}{
		{"expired", func() string {
			c := validClaims()
			c["exp"] = time.Now().Add(-time.Hour).Unix()
			return f.sign(t, jwt.SigningMethodRS256, f.key, testKID, c)
		}, jwt.ErrTokenExpired},
		{"wrong audience", func() string {
			c := validClaims()
			c["aud"] = []string{"https://someone-else.example/"}
			return f.sign(t, jwt.SigningMethodRS256, f.key, testKID, c)
		}, jwt.ErrTokenInvalidAudience},
		{"missing audience", func() string {
			c := validClaims()
			delete(c, "aud")
			return f.sign(t, jwt.SigningMethodRS256, f.key, testKID, c)
		}, jwt.ErrTokenRequiredClaimMissing},
		{"wrong issuer", func() string {
			c := validClaims()
			c["iss"] = "https://evil.example/"
			return f.sign(t, jwt.SigningMethodRS256, f.key, testKID, c)
		}, jwt.ErrTokenInvalidIssuer},
		{"missing expiration", func() string {
			c := validClaims()
			delete(c, "exp")
			return f.sign(t, jwt.SigningMethodRS256, f.key, testKID, c)
		}, jwt.ErrTokenRequiredClaimMissing},
		{"signed by another key", func() string {
			return f.sign(t, jwt.SigningMethodRS256, otherKey, testKID, validClaims())
		}, jwt.ErrTokenSignatureInvalid},
		{"unexpected algorithm", func() string {
			return f.sign(t, jwt.SigningMethodHS256, []byte("secret"), testKID, validClaims())
		}, jwt.ErrTokenSignatureInvalid},
		{"unknown kid", func() string {
			return f.sign(t, jwt.SigningMethodRS256, f.key, "other-kid", validClaims())
		}, ErrKeyNotFound},
		{"garbage", func() string { return "not.a.jwt" }, jwt.ErrTokenMalformed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.validator.Validate(t.Context(), tc.token())
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want it to wrap %v", err, tc.wantErr)
			}
		})
	}
}

func TestJWKSClientCachesKeys(t *testing.T) {
	f := newFixture(t, func(k *rsa.PrivateKey) JSONWebKey { return rawKey(k, testKID) })
	tok := f.sign(t, jwt.SigningMethodRS256, f.key, testKID, validClaims())

	for i := 0; i < 3; i++ {
		if _, err := f.validator.Validate(t.Context(), tok); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.fetches.Load(); n != 1 {
		t.Errorf("JWKS fetched %d times, want 1", n)
	}
}

func TestJWKSClientFetchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := NewJWKSClient(srv.URL, nil).Key(t.Context(), testKID); err == nil {
		t.Fatal("expected an error")
	}
}

func TestHandler(t *testing.T) {
	f := newFixture(t, func(k *rsa.PrivateKey) JSONWebKey { return rawKey(k, testKID) })
	reached := 0
	h := f.validator.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.Write([]byte("ok"))
	}))
	good := f.sign(t, jwt.SigningMethodRS256, f.key, testKID, validClaims())

	tests := []struct {
		name       string
		method     string
		authHeader string
		wantStatus int
		wantBody   string
	}{
		{"valid token", "GET", "Bearer " + good, 200, "ok"},
		{"lowercase scheme", "GET", "bearer " + good, 200, "ok"},
		{"preflight skips auth", "OPTIONS", "", 200, "ok"},
		{"no header", "GET", "", 401, `{"Message":"Invalid access token"}`},
		{"wrong scheme", "GET", "Basic " + good, 401, `{"Message":"Invalid access token"}`},
		{"extra parts", "GET", "Bearer " + good + " x", 401, `{"Message":"Invalid access token"}`},
		{"bad token", "GET", "Bearer abc", 401, `{"Message":"Invalid access token"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/members", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus || rec.Body.String() != tc.wantBody {
				t.Errorf("got %d %q, want %d %q", rec.Code, rec.Body.String(), tc.wantStatus, tc.wantBody)
			}
			if tc.wantStatus == 401 && rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Error("missing WWW-Authenticate header")
			}
		})
	}
	if reached != 3 {
		t.Errorf("wrapped handler reached %d times, want 3", reached)
	}
}
