// Package auth validates Auth0 access tokens (RS256 JWTs) against a JSON Web Key Set.
package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// ErrKeyNotFound is returned when the JWKS does not contain the requested key id.
var ErrKeyNotFound = errors.New("unable to find appropriate key")

// JSONWebKey is a single key of a JWKS: https://auth0.com/docs/jwks
type JSONWebKey struct {
	Kty string   `json:"kty"`
	Kid string   `json:"kid"`
	Use string   `json:"use"`
	N   string   `json:"n"`
	E   string   `json:"e"`
	X5c []string `json:"x5c"`
}

// JSONWebKeySet is the document served at the JWKS URL.
type JSONWebKeySet struct {
	Keys []JSONWebKey `json:"keys"`
}

// PublicKey extracts the RSA public key, preferring the x5c certificate chain
// (which is what Auth0 publishes) and falling back to the n/e parameters.
func (k JSONWebKey) PublicKey() (*rsa.PublicKey, error) {
	if len(k.X5c) > 0 {
		der, err := base64.StdEncoding.DecodeString(k.X5c[0])
		if err != nil {
			return nil, fmt.Errorf("decode x5c certificate: %w", err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("parse x5c certificate: %w", err)
		}
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("x5c certificate does not contain an RSA public key")
		}
		return pub, nil
	}

	if k.N == "" || k.E == "" {
		return nil, errors.New("key has neither x5c nor n/e parameters")
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() <= 0 || e.Int64() > 1<<31-1 {
		return nil, errors.New("invalid RSA exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}, nil
}

// KeyProvider looks up the public key for a token's 'kid' header.
type KeyProvider interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// JWKSClient fetches and caches the keys published at a JWKS URL.
type JWKSClient struct {
	url        string
	httpClient *http.Client
	// ttl is how long cached keys are served before a refresh is attempted.
	ttl time.Duration
	// minRefresh throttles refetching when a token carries an unknown kid.
	minRefresh time.Duration
	now        func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewJWKSClient creates a client for url. A nil httpClient uses a client with a 10s timeout.
func NewJWKSClient(url string, httpClient *http.Client) *JWKSClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &JWKSClient{
		url:        url,
		httpClient: httpClient,
		ttl:        15 * time.Minute,
		minRefresh: 10 * time.Second,
		now:        time.Now,
	}
}

// Key returns the public key for kid, refreshing the cache when needed.
func (c *JWKSClient) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if key, ok := c.keys[kid]; ok && now.Sub(c.fetchedAt) < c.ttl {
		return key, nil
	}

	if c.keys == nil || now.Sub(c.fetchedAt) >= c.minRefresh {
		if err := c.refresh(ctx); err != nil {
			// keep serving a stale key rather than locking everybody out
			if key, ok := c.keys[kid]; ok {
				return key, nil
			}
			return nil, err
		}
		c.fetchedAt = now
	}

	if key, ok := c.keys[kid]; ok {
		return key, nil
	}
	return nil, ErrKeyNotFound
}

func (c *JWKSClient) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS: unexpected status %d", resp.StatusCode)
	}

	var set JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "" && k.Kty != "RSA" {
			continue
		}
		pub, err := k.PublicKey()
		if err != nil {
			// skip keys we cannot use; others may still be fine
			continue
		}
		keys[k.Kid] = pub
	}

	c.keys = keys
	return nil
}
