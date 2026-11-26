package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewJWKSKeySourceRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		client  *http.Client
		url     string
		timeout time.Duration
	}{
		{name: "nil client", url: "https://keys.example/jwks", timeout: time.Second},
		{name: "invalid URL", client: http.DefaultClient, url: ":bad", timeout: time.Second},
		{name: "unsupported scheme", client: http.DefaultClient, url: "file:///keys", timeout: time.Second},
		{name: "missing host", client: http.DefaultClient, url: "https:///keys", timeout: time.Second},
		{name: "invalid timeout", client: http.DefaultClient, url: "https://keys.example/jwks"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewJWKSKeySource(test.client, test.url, test.timeout); err == nil {
				t.Fatal("NewJWKSKeySource() error = nil")
			}
		})
	}
}

func TestJWKSKeySourceParsesModulusAndCertificate(t *testing.T) {
	privateKey := mustRSAKey(t)
	certificate := mustCertificate(t, privateKey)
	tests := []struct {
		name string
		jwk  map[string]any
	}{
		{name: "modulus and exponent", jwk: rsaJWK("modulus", &privateKey.PublicKey)},
		{name: "certificate", jwk: map[string]any{
			"kty": "RSA", "kid": "certificate", "use": "sig", "alg": "RS256", "x5c": []string{certificate},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := jwksServer(t, http.StatusOK, marshalJWKS(t, test.jwk))
			defer server.Close()
			source := mustJWKSKeySource(t, server.URL, time.Second)
			kid := test.jwk["kid"].(string)
			key, err := source.Key(context.Background(), kid)
			if err != nil {
				t.Fatalf("Key() error = %v", err)
			}
			if key.N.Cmp(privateKey.N) != 0 || key.E != privateKey.E {
				t.Fatal("Key() returned the wrong public key")
			}
		})
	}
}

func TestJWKSKeySourceRejectsMalformedKeys(t *testing.T) {
	privateKey := mustRSAKey(t)
	valid := rsaJWK("key", &privateKey.PublicKey)
	tests := []struct {
		name string
		keys []map[string]any
	}{
		{name: "empty keys", keys: []map[string]any{}},
		{name: "missing kid", keys: []map[string]any{{"kty": "RSA", "use": "sig", "n": valid["n"], "e": valid["e"]}}},
		{name: "duplicate kid", keys: []map[string]any{valid, valid}},
		{name: "wrong type", keys: []map[string]any{{"kty": "EC", "kid": "key", "use": "sig", "n": valid["n"], "e": valid["e"]}}},
		{name: "wrong use", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "enc", "n": valid["n"], "e": valid["e"]}}},
		{name: "wrong algorithm", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "alg": "RS512", "n": valid["n"], "e": valid["e"]}}},
		{name: "empty x5c", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "x5c": []string{}}}},
		{name: "null x5c", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "x5c": nil}}},
		{name: "malformed x5c type", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "x5c": "certificate"}}},
		{name: "malformed x5c base64", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "x5c": []string{"%%%"}}}},
		{name: "malformed x5c certificate", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "x5c": []string{base64.StdEncoding.EncodeToString([]byte("not a certificate"))}}}},
		{name: "malformed x5c chain entry", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "x5c": []string{mustCertificate(t, privateKey), ""}}}},
		{name: "missing modulus", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "e": valid["e"]}}},
		{name: "malformed modulus", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "n": "%%%", "e": valid["e"]}}},
		{name: "missing exponent", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "n": valid["n"]}}},
		{name: "malformed exponent", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "n": valid["n"], "e": "%%%"}}},
		{name: "even exponent", keys: []map[string]any{{"kty": "RSA", "kid": "key", "use": "sig", "n": valid["n"], "e": base64.RawURLEncoding.EncodeToString([]byte{4})}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"keys": test.keys})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			server := jwksServer(t, http.StatusOK, string(body))
			defer server.Close()
			source := mustJWKSKeySource(t, server.URL, time.Second)
			if _, err := source.Key(context.Background(), "key"); !errors.Is(err, ErrInvalidJWKS) {
				t.Fatalf("Key() error = %v, want ErrInvalidJWKS", err)
			}
		})
	}
}

func TestJWKSKeySourceResponseFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non success", status: http.StatusBadGateway, body: `{}`},
		{name: "invalid JSON", status: http.StatusOK, body: `{`},
		{name: "trailing JSON", status: http.StatusOK, body: `{"keys":[]} {}`},
		{name: "oversized", status: http.StatusOK, body: strings.Repeat("x", int(maxJWKSBodyBytes)+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := jwksServer(t, test.status, test.body)
			defer server.Close()
			source := mustJWKSKeySource(t, server.URL, time.Second)
			if _, err := source.Key(context.Background(), "key"); !errors.Is(err, ErrInvalidJWKS) {
				t.Fatalf("Key() error = %v, want ErrInvalidJWKS", err)
			}
		})
	}
}

func TestJWKSKeySourceTimeout(t *testing.T) {
	deadlineSeen := false
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		_, deadlineSeen = request.Context().Deadline()
		return nil, context.DeadlineExceeded
	})}
	source, err := NewJWKSKeySource(client, "https://jwks.example.test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Key(context.Background(), "key"); !errors.Is(err, ErrInvalidJWKS) {
		t.Fatalf("Key() error = %v, want ErrInvalidJWKS", err)
	}
	if !deadlineSeen {
		t.Fatal("JWKS request context has no deadline")
	}
}

func TestJWKSKeySourceCacheReuseAndUnknownKIDRefresh(t *testing.T) {
	first := mustRSAKey(t)
	second := mustRSAKey(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit := hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if hit == 1 {
			_, _ = w.Write([]byte(marshalJWKS(t, rsaJWK("first", &first.PublicKey))))
			return
		}
		_, _ = w.Write([]byte(marshalJWKS(t, rsaJWK("first", &first.PublicKey), rsaJWK("second", &second.PublicKey))))
	}))
	defer server.Close()
	source := mustJWKSKeySource(t, server.URL, time.Second)

	for index := 0; index < 2; index++ {
		if _, err := source.Key(context.Background(), "first"); err != nil {
			t.Fatalf("first Key() error = %v", err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("requests after cache reuse = %d, want 1", hits.Load())
	}
	key, err := source.Key(context.Background(), "second")
	if err != nil {
		t.Fatalf("second Key() error = %v", err)
	}
	if key.N.Cmp(second.N) != 0 || hits.Load() != 2 {
		t.Fatalf("refresh requests = %d or wrong refreshed key", hits.Load())
	}
	if _, err := source.Key(context.Background(), "unknown"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("unknown Key() error = %v, want ErrKeyNotFound", err)
	}
}

func TestJWKSKeySourceConcurrentCacheSafety(t *testing.T) {
	privateKey := mustRSAKey(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(marshalJWKS(t, rsaJWK("key", &privateKey.PublicKey))))
	}))
	defer server.Close()
	source := mustJWKSKeySource(t, server.URL, time.Second)

	const goroutines = 32
	start := make(chan struct{})
	errorsFound := make(chan error, goroutines)
	var wait sync.WaitGroup
	wait.Add(goroutines)
	for index := 0; index < goroutines; index++ {
		go func() {
			defer wait.Done()
			<-start
			key, err := source.Key(context.Background(), "key")
			if err == nil && key.N.Cmp(privateKey.N) != 0 {
				err = errors.New("wrong public key")
			}
			errorsFound <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("concurrent Key() error = %v", err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("JWKS requests = %d, want 1", got)
	}
}

func TestJWKSKeySourceConcurrentUnknownKIDRefreshesOnce(t *testing.T) {
	privateKey := mustRSAKey(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(marshalJWKS(t, rsaJWK("known", &privateKey.PublicKey))))
	}))
	defer server.Close()
	source := mustJWKSKeySource(t, server.URL, time.Second)
	if _, err := source.Key(context.Background(), "known"); err != nil {
		t.Fatalf("initial Key() error = %v", err)
	}

	const goroutines = 32
	start := make(chan struct{})
	errorsFound := make(chan error, goroutines)
	var wait sync.WaitGroup
	wait.Add(goroutines)
	for index := 0; index < goroutines; index++ {
		go func() {
			defer wait.Done()
			<-start
			_, err := source.Key(context.Background(), "unknown")
			errorsFound <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("concurrent unknown Key() error = %v", err)
		}
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("JWKS requests = %d, want initial fetch plus one refresh", got)
	}
}

func TestJWKSFailedRefreshPreservesCachedKeys(t *testing.T) {
	privateKey := mustRSAKey(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			_, _ = w.Write([]byte(marshalJWKS(t, rsaJWK("key", &privateKey.PublicKey))))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	source := mustJWKSKeySource(t, server.URL, time.Second)
	if _, err := source.Key(context.Background(), "key"); err != nil {
		t.Fatalf("initial Key() error = %v", err)
	}
	if _, err := source.Key(context.Background(), "unknown"); !errors.Is(err, ErrInvalidJWKS) {
		t.Fatalf("refresh Key() error = %v", err)
	}
	if _, err := source.Key(context.Background(), "key"); err != nil {
		t.Fatalf("cached Key() after failed refresh error = %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("JWKS requests = %d, want 2", hits.Load())
	}
}

func rsaJWK(kid string, key *rsa.PublicKey) map[string]any {
	exponent := big.NewInt(int64(key.E)).Bytes()
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(exponent),
	}
}

func mustCertificate(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("x509.CreateCertificate() error = %v", err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func marshalJWKS(t *testing.T, keys ...map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(body)
}

func jwksServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func mustJWKSKeySource(t *testing.T, url string, timeout time.Duration) *JWKSKeySource {
	t.Helper()
	source, err := NewJWKSKeySource(http.DefaultClient, url, timeout)
	if err != nil {
		t.Fatalf("NewJWKSKeySource() error = %v", err)
	}
	return source
}

func TestJWKSKeySourceRejectsInvalidRequestedKID(t *testing.T) {
	source := &JWKSKeySource{}
	for _, kid := range []string{"", " key", "key "} {
		if _, err := source.Key(context.Background(), kid); !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Key(%q) error = %v", kid, err)
		}
	}
}

func TestJWKSKeySourceHonorsParentCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	source := mustJWKSKeySource(t, server.URL, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Key(ctx, "key"); !errors.Is(err, ErrInvalidJWKS) {
		t.Fatalf("Key() error = %v", err)
	}
}

func TestJWKSKeySourceUsesGETAndJSONAccept(t *testing.T) {
	privateKey := mustRSAKey(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Accept") != "application/json" {
			t.Errorf("request = %s Accept %q", r.Method, r.Header.Get("Accept"))
		}
		_, _ = fmt.Fprint(w, marshalJWKS(t, rsaJWK("key", &privateKey.PublicKey)))
	}))
	defer server.Close()
	source := mustJWKSKeySource(t, server.URL, time.Second)
	if _, err := source.Key(context.Background(), "key"); err != nil {
		t.Fatalf("Key() error = %v", err)
	}
}
