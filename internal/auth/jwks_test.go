package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestJWKSProviderCachesRefreshesUnknownKeysAndRotates(t *testing.T) {
	first := generateTestKey(t)
	second := generateTestKey(t)
	var requests atomic.Int32
	var stateMu sync.RWMutex
	body := marshalJWKS(t, rsaJWK("first", &first.PublicKey))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		stateMu.RLock()
		defer stateMu.RUnlock()
		_, _ = w.Write(body)
	}))
	defer server.Close()

	provider, err := NewJWKSProvider(JWKSConfig{URL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	assertKeyMatches(t, provider, "first", &first.PublicKey)
	assertKeyMatches(t, provider, "first", &first.PublicKey)
	if got := requests.Load(); got != 1 {
		t.Fatalf("cached key made %d requests, want 1", got)
	}

	if _, err := provider.Key(context.Background(), "absent"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("unknown key error = %v, want ErrKeyNotFound", err)
	}
	if _, err := provider.Key(context.Background(), "absent"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("cached unknown key error = %v, want ErrKeyNotFound", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("repeated unknown key made %d requests, want 2 total", got)
	}

	stateMu.Lock()
	body = marshalJWKS(t, rsaJWK("first", &first.PublicKey), rsaJWK("second", &second.PublicKey))
	stateMu.Unlock()
	assertKeyMatches(t, provider, "second", &second.PublicKey)
	if got := requests.Load(); got != 3 {
		t.Fatalf("rotation made %d requests, want 3 total", got)
	}
}

func TestJWKSProviderCoalescesConcurrentRefresh(t *testing.T) {
	key := generateTestKey(t)
	body := marshalJWKS(t, rsaJWK("shared", &key.PublicKey))
	var requests atomic.Int32
	requestStarted := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(requestStarted)
		}
		<-release
		_, _ = w.Write(body)
	}))
	defer server.Close()

	provider, err := NewJWKSProvider(JWKSConfig{URL: server.URL, Client: server.Client(), RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 32
	start := make(chan struct{})
	errorsByCall := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			_, err := provider.Key(context.Background(), "shared")
			errorsByCall <- err
		}()
	}
	close(start)
	<-requestStarted
	close(release)
	for i := 0; i < goroutines; i++ {
		if err := <-errorsByCall; err != nil {
			t.Fatalf("concurrent Key() = %v", err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("concurrent lookups made %d requests, want 1", got)
	}
}

func TestJWKSProviderRejectsMalformedDuplicateAndUnsupportedKeys(t *testing.T) {
	key := generateTestKey(t)
	valid := rsaJWK("one", &key.PublicKey)
	evenExponent := rsaJWK("one", &key.PublicKey)
	evenExponent["e"] = base64.RawURLEncoding.EncodeToString(big.NewInt(2).Bytes())
	shortModulus := rsaJWK("one", &key.PublicKey)
	shortModulus["n"] = base64.RawURLEncoding.EncodeToString(big.NewInt(3).Bytes())

	tests := []struct {
		name    string
		body    []byte
		maxKeys int
	}{
		{name: "invalid JSON", body: []byte(`{"keys":[`)},
		{name: "empty set", body: []byte(`{"keys":[]}`)},
		{name: "duplicate key ID", body: marshalJWKS(t, valid, valid)},
		{name: "unsupported key type", body: marshalJWKS(t, map[string]any{"kty": "EC", "kid": "one"})},
		{name: "unsupported use", body: marshalJWKS(t, mergeJWK(valid, "use", "enc"))},
		{name: "unsupported algorithm", body: marshalJWKS(t, mergeJWK(valid, "alg", "RS384"))},
		{name: "unsupported operation", body: marshalJWKS(t, mergeJWK(valid, "key_ops", []string{"sign"}))},
		{name: "missing key ID", body: marshalJWKS(t, mergeJWK(valid, "kid", ""))},
		{name: "invalid modulus", body: marshalJWKS(t, mergeJWK(valid, "n", "!"))},
		{name: "short modulus", body: marshalJWKS(t, shortModulus)},
		{name: "invalid exponent", body: marshalJWKS(t, evenExponent)},
		{
			name: "too many keys", maxKeys: 1,
			body: marshalJWKS(t, valid, rsaJWK("two", &key.PublicKey)),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(body)
			}))
			defer server.Close()
			provider, err := NewJWKSProvider(JWKSConfig{
				URL: server.URL, Client: server.Client(), MaxKeys: test.maxKeys,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.Key(context.Background(), "one"); !errors.Is(err, ErrProviderUnavailable) {
				t.Fatalf("Key(malformed JWKS) = %v, want ErrProviderUnavailable", err)
			}
			provider.mu.Lock()
			cached := len(provider.keys)
			provider.mu.Unlock()
			if cached != 0 {
				t.Fatalf("malformed JWKS cached %d keys", cached)
			}
		})
	}
}

func TestJWKSProviderBoundsResponseAndRequestTime(t *testing.T) {
	t.Run("oversized response", func(t *testing.T) {
		secret := "provider-body-secret"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat(secret, 100)))
		}))
		defer server.Close()
		provider, err := NewJWKSProvider(JWKSConfig{
			URL: server.URL + "/url-secret", Client: server.Client(), MaxResponseBytes: 64,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = provider.Key(context.Background(), "secret-key-id")
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("oversized response error = %v", err)
		}
		for _, leaked := range []string{secret, "url-secret", "secret-key-id"} {
			if strings.Contains(err.Error(), leaked) {
				t.Fatalf("provider error leaked %q: %v", leaked, err)
			}
		}
	})

	t.Run("request timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()
		provider, err := NewJWKSProvider(JWKSConfig{
			URL: server.URL, Client: server.Client(), RequestTimeout: 20 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		if _, err := provider.Key(context.Background(), "key"); !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("timeout error = %v", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("bounded request took %v", elapsed)
		}
	})
}

func TestJWKSProviderBoundsRetriesAfterProviderFailure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "upstream-secret", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	provider, err := NewJWKSProvider(JWKSConfig{
		URL:          server.URL,
		Client:       server.Client(),
		FailureRetry: time.Minute,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := provider.Key(context.Background(), "unknown"); !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("failed provider lookup %d = %v", i, err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("failure cooldown made %d requests, want 1", got)
	}
	now = now.Add(2 * time.Minute)
	if _, err := provider.Key(context.Background(), "unknown"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("retry after cooldown = %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("retry after cooldown made %d requests, want 2", got)
	}
}

func TestJWKSProviderReturnsIndependentCachedKeys(t *testing.T) {
	key := generateTestKey(t)
	body := marshalJWKS(t, rsaJWK("one", &key.PublicKey))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()
	provider, err := NewJWKSProvider(JWKSConfig{URL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	got, err := provider.Key(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	got.N.SetInt64(1)
	assertKeyMatches(t, provider, "one", &key.PublicKey)
}

func assertKeyMatches(t *testing.T, provider *JWKSProvider, kid string, want *rsa.PublicKey) {
	t.Helper()
	got, err := provider.Key(context.Background(), kid)
	if err != nil {
		t.Fatalf("Key(%q) = %v", kid, err)
	}
	if got.E != want.E || got.N.Cmp(want.N) != 0 {
		t.Fatalf("Key(%q) does not match published RSA key", kid)
	}
}

func mergeJWK(source map[string]any, key string, value any) map[string]any {
	copy := make(map[string]any, len(source))
	for existingKey, existingValue := range source {
		copy[existingKey] = existingValue
	}
	copy[key] = value
	return copy
}
